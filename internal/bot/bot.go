package bot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"snt-bot/internal/ai"
	"snt-bot/internal/config"
	"snt-bot/internal/db"
	"snt-bot/internal/distribution"
	"snt-bot/internal/state"
)

type pendingConfirm struct {
	rows   []distribution.DistributionRow
	curBal float64
}

type validatedFields struct {
	Date        string
	Direction   string
	PaymentType string
	Plot        string
	Category    string
	Note        string
	Membership  string
	Amount      float64
}

type Bot struct {
	api     *tgbotapi.BotAPI
	db      *sql.DB
	cfg     *config.Config
	client  *ai.Client
	states  *state.Manager
	mu      sync.Mutex
	pending map[int64]*pendingConfirm
}

func New(token string, sqlDB *sql.DB, cfg *config.Config, client *ai.Client, states *state.Manager) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}
	return &Bot{
		api:     api,
		db:      sqlDB,
		cfg:     cfg,
		client:  client,
		states:  states,
		pending: make(map[int64]*pendingConfirm),
	}, nil
}

func (b *Bot) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		if update.CallbackQuery != nil {
			b.handleCallback(update.CallbackQuery)
			continue
		}
		if update.Message == nil {
			continue
		}
		if !b.cfg.IsAllowedUser(update.Message.From.ID) {
			b.send(update.Message.Chat.ID, "Доступ запрещён.")
			continue
		}
		b.handleMessage(update.Message)
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	if text == "Отмена" || (msg.IsCommand() && msg.Command() == "start") {
		b.states.Clear(userID)
		b.clearPending(userID)
		b.sendMenu(chatID, "Главное меню.")
		return
	}

	st := b.states.Get(userID)

	switch text {
	case "Добавить операцию":
		if st.Phase == state.PhaseAdding {
			b.send(chatID, "Уже в процессе добавления. Начинаю заново.")
		}
		st.Phase = state.PhaseAdding
		st.History = nil
		b.states.Set(userID, st)
		b.handleAdding(chatID, userID, "")
		return
	case "Баланс":
		st.Phase = state.PhaseBalance
		st.History = nil
		b.states.Set(userID, st)
		b.send(chatID, "Введите количество последних операций для отображения:")
		return
	case "Экспорт":
		st.Phase = state.PhaseExport
		st.History = nil
		b.states.Set(userID, st)
		b.send(chatID, "Введите количество последних строк для экспорта:")
		return
	}

	switch st.Phase {
	case state.PhaseAdding:
		b.handleAdding(chatID, userID, text)
	case state.PhaseBalance:
		b.handleBalanceN(chatID, userID, text)
	case state.PhaseExport:
		b.handleExportN(chatID, userID, text)
	default:
		b.sendMenu(chatID, "Выберите действие.")
	}
}

func (b *Bot) handleAdding(chatID, userID int64, text string) {
	st := b.states.Get(userID)
	if text != "" {
		st.History = append(st.History, ai.Msg{Role: "user", Content: text})
		b.states.Set(userID, st)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := b.client.Call(ctx, st.History)
	if err != nil {
		log.Printf("ai call error user %d: %v", userID, err)
		b.send(chatID, "Ошибка связи с AI. Попробуйте ещё раз.")
		return
	}

	st.History = append(st.History, ai.Msg{Role: "assistant", Content: resp.Message})
	b.states.Set(userID, st)

	switch resp.Status {
	case "abort":
		b.states.Clear(userID)
		b.clearPending(userID)
		b.sendMenu(chatID, resp.Message)
	case "ready":
		b.handleReady(chatID, userID, resp.Fields)
	default: // extracting
		b.send(chatID, resp.Message)
	}
}

func (b *Bot) handleReady(chatID, userID int64, fields ai.Fields) {
	vf, errMsg := b.validateFields(fields)
	if errMsg != "" {
		st := b.states.Get(userID)
		st.History = append(st.History, ai.Msg{
			Role:    "system",
			Content: "Ошибка валидации: " + errMsg + ". Уточни у пользователя.",
		})
		b.states.Set(userID, st)
		b.handleAdding(chatID, userID, "")
		return
	}

	year := parseYear(vf.Date)

	rows, err := b.buildRows(vf, year)
	if err != nil {
		log.Printf("build rows user %d: %v", userID, err)
		b.send(chatID, "Ошибка формирования операции: "+err.Error())
		b.states.Clear(userID)
		b.sendMenu(chatID, "")
		return
	}

	bal := b.effectiveBalance()
	b.setPending(userID, &pendingConfirm{rows: rows, curBal: bal})

	m := tgbotapi.NewMessage(chatID, formatPreview(rows, bal))
	m.ReplyMarkup = confirmKeyboard()
	if _, err := b.api.Send(m); err != nil {
		log.Printf("send confirm user %d: %v", userID, err)
	}
}

func (b *Bot) buildRows(vf validatedFields, year int) ([]distribution.DistributionRow, error) {
	if vf.Direction == "приход" && len(b.cfg.ContributionAmounts) > 0 && vf.Membership != "-" {
		alreadyPaid, err := db.GetOutstanding(b.db, vf.Plot, year)
		if err != nil {
			return nil, fmt.Errorf("get outstanding: %w", err)
		}

		priorities := b.cfg.PriorityFor(vf.Membership)

		outstanding := make(map[string]float64, len(priorities))
		for _, id := range priorities {
			outstanding[id] = computeOutstanding(b.cfg.ContributionAmounts[id], alreadyPaid[id])
		}

		nextYearDue := make(map[string]float64, len(priorities))
		for _, id := range priorities {
			nextYearDue[id] = b.cfg.ContributionAmounts[id]
		}

		return distribution.ComputeDistribution(
			distribution.OperationFields{
				Date:        vf.Date,
				Direction:   vf.Direction,
				PaymentType: vf.PaymentType,
				Plot:        vf.Plot,
				Category:    vf.Category,
				Note:        vf.Note,
				Membership:  vf.Membership,
				Amount:      vf.Amount,
			},
			outstanding, priorities, year, nextYearDue,
		)
	}

	return []distribution.DistributionRow{{
		ContributionID: vf.Category,
		Membership:     vf.Membership,
		Plot:           vf.Plot,
		PaymentType:    vf.PaymentType,
		OpDate:         vf.Date,
		Category:       vf.Category,
		Note:           vf.Note,
		Direction:      vf.Direction,
		Amount:         vf.Amount,
		FiscalYear:     year,
	}}, nil
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	chatID := cb.Message.Chat.ID
	userID := cb.From.ID

	if _, err := b.api.Request(tgbotapi.NewCallback(cb.ID, "")); err != nil {
		log.Printf("ack callback: %v", err)
	}

	switch cb.Data {
	case "op_confirm":
		p := b.getPending(userID)
		if p == nil {
			b.send(chatID, "Нет ожидающей операции.")
			return
		}

		if err := distribution.CommitDistribution(b.db, p.rows, b.cfg.InitialBalance); err != nil {
			log.Printf("commit distribution user %d: %v", userID, err)
			b.send(chatID, "Ошибка записи в базу данных.")
			return
		}

		b.clearPending(userID)
		b.states.Clear(userID)

		bal, _ := db.GetBalance(b.db)
		b.sendMenu(chatID, fmt.Sprintf("Операция записана. Баланс: %.2f руб.", bal))

	case "op_cancel":
		b.clearPending(userID)
		b.states.Clear(userID)
		b.sendMenu(chatID, "Отменено.")
	}
}

func (b *Bot) handleBalanceN(chatID, userID int64, text string) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n <= 0 {
		b.send(chatID, "Введите целое положительное число.")
		return
	}

	bal := b.effectiveBalance()

	income, expense, err := db.GetTotals(b.db)
	if err != nil {
		b.send(chatID, "Ошибка получения итогов.")
		return
	}

	ops, err := db.LastNOperations(b.db, n)
	if err != nil {
		b.send(chatID, "Ошибка получения операций.")
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Баланс: %.2f руб.\nПриход всего: %.2f руб.\nРасход всего: %.2f руб.\n\n",
		bal, income, expense))
	sb.WriteString(fmt.Sprintf("Последние %d операций:\n", len(ops)))
	for _, op := range ops {
		sign := "+"
		if op.Direction == "расход" {
			sign = "-"
		}
		sb.WriteString(fmt.Sprintf("%s  %s%.2f  %s  %s  %s  %s\n",
			op.OpDate, sign, op.Amount, op.Category, op.Plot, op.Membership, op.PaymentType))
	}

	b.states.Clear(userID)
	b.sendMenu(chatID, sb.String())
}

func (b *Bot) handleExportN(chatID, userID int64, text string) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n <= 0 {
		b.send(chatID, "Введите целое положительное число.")
		return
	}

	total, err := db.TotalCount(b.db)
	if err != nil {
		b.send(chatID, "Ошибка получения данных.")
		return
	}
	if total == 0 {
		b.states.Clear(userID)
		b.sendMenu(chatID, "Нет данных для экспорта.")
		return
	}
	if n > total {
		n = total
	}

	ops, err := db.LastNRowsAsc(b.db, n)
	if err != nil {
		b.send(chatID, "Ошибка получения данных.")
		return
	}

	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Членство", "Дата", "Приход", "Расход", "Тип платежа", "Участок", "Категория", "Остаток", "Примечание"})
	for _, op := range ops {
		inc, exp := "", ""
		if op.Direction == "приход" {
			inc = fmt.Sprintf("%.2f", op.Amount)
		} else {
			exp = fmt.Sprintf("%.2f", op.Amount)
		}
		_ = w.Write([]string{
			op.Membership, op.OpDate, inc, exp,
			op.PaymentType, op.Plot, op.Category,
			fmt.Sprintf("%.2f", op.BalanceAfter),
			op.Note,
		})
	}
	w.Flush()

	doc := tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{
		Name:  fmt.Sprintf("export_%s.csv", time.Now().Format("20060102_150405")),
		Bytes: buf.Bytes(),
	})
	if _, err := b.api.Send(doc); err != nil {
		log.Printf("send export user %d: %v", userID, err)
	}

	b.states.Clear(userID)
	b.sendMenu(chatID, fmt.Sprintf("Экспортировано %d строк.", len(ops)))
}

func (b *Bot) validateFields(f ai.Fields) (validatedFields, string) {
	if f.Date == nil {
		return validatedFields{}, "дата не указана"
	}
	if _, err := time.Parse("02.01.2006", *f.Date); err != nil {
		return validatedFields{}, "неверный формат даты, ожидается DD.MM.YYYY"
	}
	if f.Direction == nil || (*f.Direction != "приход" && *f.Direction != "расход") {
		return validatedFields{}, "направление должно быть 'приход' или 'расход'"
	}
	amount := f.AmountFloat()
	if amount <= 0 {
		return validatedFields{}, "сумма должна быть положительной"
	}
	if f.PaymentType == nil || !contains(b.cfg.PaymentTypes, *f.PaymentType) {
		return validatedFields{}, "недопустимый тип платежа"
	}
	if f.Plot == nil || !contains(b.cfg.Plots, *f.Plot) {
		return validatedFields{}, "недопустимый участок"
	}
	cats := b.cfg.CategoriesIncome
	if *f.Direction == "расход" {
		cats = b.cfg.CategoriesExpense
	}
	if f.Category == nil || !contains(cats, *f.Category) {
		return validatedFields{}, fmt.Sprintf("недопустимая категория для направления '%s'", *f.Direction)
	}
	note := ""
	if f.Note != nil {
		note = *f.Note
	}
	return validatedFields{
		Date:        *f.Date,
		Direction:   *f.Direction,
		Amount:      amount,
		PaymentType: *f.PaymentType,
		Plot:        *f.Plot,
		Category:    *f.Category,
		Note:        note,
		Membership:  b.cfg.PlotMembership(*f.Plot),
	}, ""
}

func (b *Bot) effectiveBalance() float64 {
	count, _ := db.TotalCount(b.db)
	if count == 0 {
		return b.cfg.InitialBalance
	}
	bal, _ := db.GetBalance(b.db)
	return bal
}

func (b *Bot) send(chatID int64, text string) {
	if _, err := b.api.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		log.Printf("send chatID %d: %v", chatID, err)
	}
}

func (b *Bot) sendMenu(chatID int64, text string) {
	if text == "" {
		text = "Главное меню."
	}
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyMarkup = mainKeyboard()
	if _, err := b.api.Send(m); err != nil {
		log.Printf("send menu chatID %d: %v", chatID, err)
	}
}

func (b *Bot) setPending(userID int64, p *pendingConfirm) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[userID] = p
}

func (b *Bot) getPending(userID int64) *pendingConfirm {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending[userID]
}

func (b *Bot) clearPending(userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, userID)
}

func mainKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("Добавить операцию"),
			tgbotapi.NewKeyboardButton("Баланс"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("Экспорт"),
			tgbotapi.NewKeyboardButton("Отмена"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

func confirmKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Подтвердить ✓", "op_confirm"),
			tgbotapi.NewInlineKeyboardButtonData("Отмена ✗", "op_cancel"),
		),
	)
}

func formatPreview(rows []distribution.DistributionRow, curBal float64) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Предпросмотр (%d строк):\n\n", len(rows)))
	bal := curBal
	for _, r := range rows {
		if r.Direction == "приход" {
			bal += r.Amount
		} else {
			bal -= r.Amount
		}
		sb.WriteString(fmt.Sprintf("• %s  %.2f руб.  %d г.  %s/%s → %.2f\n",
			r.ContributionID, r.Amount, r.FiscalYear, r.Membership, r.Plot, bal))
	}
	sb.WriteString(fmt.Sprintf("\nФинальный баланс: %.2f руб.", bal))
	return sb.String()
}

func computeOutstanding(annualDue, alreadyPaid float64) float64 {
	if d := annualDue - alreadyPaid; d > 0 {
		return d
	}
	return 0
}

func parseYear(date string) int {
	parts := strings.Split(date, ".")
	if len(parts) != 3 {
		return time.Now().Year()
	}
	y, err := strconv.Atoi(parts[2])
	if err != nil {
		return time.Now().Year()
	}
	return y
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
