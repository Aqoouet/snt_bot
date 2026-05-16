package bot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"regexp"
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
	rows      []distribution.DistributionRow
	curBal    float64
	createdAt time.Time
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
	api                 *tgbotapi.BotAPI
	db                  *sql.DB
	cfg                 *config.Config
	client              *ai.Client
	states              *state.Manager
	mu                  sync.Mutex
	pending             map[int64]*pendingConfirm
	busy                map[int64]bool
	plotSysPrompt       string
	extractionSysPrompt string
	buildTime           string
}

func New(token string, sqlDB *sql.DB, cfg *config.Config, client *ai.Client, states *state.Manager, plotSysPrompt, extractionSysPrompt string, buildTime string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}
	return &Bot{
		api:                 api,
		db:                  sqlDB,
		cfg:                 cfg,
		client:              client,
		states:              states,
		pending:             make(map[int64]*pendingConfirm),
		busy:                make(map[int64]bool),
		plotSysPrompt:       plotSysPrompt,
		extractionSysPrompt: extractionSysPrompt,
		buildTime:           buildTime,
	}, nil
}

func (b *Bot) Stop() {
	b.api.StopReceivingUpdates()
}

func (b *Bot) Run() {
	msg := fmt.Sprintf("Бот запущен. Сборка: %s", b.buildTime)
	log.Printf("startup notify: %q to %v", msg, b.cfg.TelegramAllowedUserIDs)
	for _, uid := range b.cfg.TelegramAllowedUserIDs {
		b.send(uid, msg)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		update := update
		if update.CallbackQuery != nil {
			go b.handleCallback(update.CallbackQuery)
			continue
		}
		if update.Message == nil {
			continue
		}
		if !b.cfg.IsAllowedUser(update.Message.From.ID) {
			b.send(update.Message.Chat.ID, "Доступ запрещён.")
			continue
		}
		go b.handleMessage(update.Message)
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	log.Printf("[IN] userID=%d chatID=%d text=%q", userID, chatID, text)

	if text == "Отмена" || (msg.IsCommand() && msg.Command() == "start") {
		b.states.Clear(userID)
		b.clearPending(userID)
		b.clearBusy(userID)
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
		st.PlotID = ""
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

	// Branch 1: No history AND no plot yet — send initial prompt, no AI call needed.
	if len(st.History) == 0 && st.PlotID == "" {
		const prompt = "Введите номер вашего участка."
		st.History = append(st.History, ai.Msg{Role: "assistant", Content: prompt})
		b.states.Set(userID, st)
		b.send(chatID, prompt)
		return
	}

	// Branch 2: Plot not yet confirmed — run plot extraction phase.
	if st.PlotID == "" {
		b.handlePlotExtraction(chatID, userID, text)
		return
	}

	// Branch 3: Plot confirmed — run main extraction phase.
	b.handleAddingMain(chatID, userID, text)
}

func (b *Bot) handlePlotExtraction(chatID, userID int64, text string) {
	st := b.states.Get(userID)

	if text != "" {
		st.History = append(st.History, ai.Msg{Role: "user", Content: text})
		b.states.Set(userID, st)
	}

	b.mu.Lock()
	if b.busy[userID] {
		b.mu.Unlock()
		b.send(chatID, "Обрабатываю предыдущий запрос, подождите...")
		return
	}
	b.busy[userID] = true
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	resp, err := b.client.CallPlot(ctx, b.plotSysPrompt, st.History)

	b.mu.Lock()
	delete(b.busy, userID)
	b.mu.Unlock()

	if err != nil {
		log.Printf("plot extraction error user %d: %v", userID, err)
		b.send(chatID, "Ошибка связи с AI. Попробуйте ещё раз.")
		return
	}

	switch resp.Status {
	case "abort":
		b.states.Clear(userID)
		b.clearPending(userID)
		b.sendMenu(chatID, resp.Message)
	case "ready":
		st = b.states.Get(userID)
		st.PlotID = resp.Plot
		st.History = filterUserMessages(st.History)
		b.states.Set(userID, st)
		b.handleAddingMain(chatID, userID, "")
	default: // extracting
		st = b.states.Get(userID)
		st.History = append(st.History, ai.Msg{Role: "assistant", Content: resp.Message})
		b.states.Set(userID, st)
		b.send(chatID, resp.Message)
	}
}

func filterUserMessages(history []ai.Msg) []ai.Msg {
	var out []ai.Msg
	for _, m := range history {
		if m.Role == "user" {
			out = append(out, m)
		}
	}
	return out
}

func (b *Bot) handleAddingMain(chatID, userID int64, text string) {
	st := b.states.Get(userID)

	if text != "" {
		st.RetryCount = 0
		st.History = append(st.History, ai.Msg{Role: "user", Content: text})
		b.states.Set(userID, st)
	}

	membership := b.cfg.PlotMembership(st.PlotID)
	year := time.Now().Year()
	payCtx := b.buildPaymentContext(st.PlotID, membership, year)
	sysPrompt := strings.ReplaceAll(b.extractionSysPrompt, "{{PAYMENT_CONTEXT}}", payCtx)

	b.mu.Lock()
	if b.busy[userID] {
		b.mu.Unlock()
		b.send(chatID, "Обрабатываю предыдущий запрос, подождите...")
		return
	}
	b.busy[userID] = true
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	resp, err := b.client.CallWithSysPrompt(ctx, sysPrompt, st.History)

	b.mu.Lock()
	delete(b.busy, userID)
	b.mu.Unlock()

	if err != nil {
		log.Printf("ai call error user %d: %v", userID, err)
		b.send(chatID, "Ошибка связи с AI. Попробуйте ещё раз.")
		return
	}

	st = b.states.Get(userID)
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

func (b *Bot) buildPaymentContext(plot, membership string, year int) string {
	dues := b.cfg.DuesFor(membership)
	if len(dues) == 0 {
		return ""
	}

	categories := config.SortedCategories(dues)
	plotCount := b.cfg.PlotCount(plot)

	outstanding, err := db.GetOutstanding(b.db, plot, year)
	if err != nil {
		log.Printf("buildPaymentContext GetOutstanding: %v", err)
		outstanding = map[string]float64{}
	}

	var sb strings.Builder
	sb.WriteString("## Контекст платежей участка\n\n")
	sb.WriteString("| Категория | Лимит | Оплачено | Остаток |\n")
	sb.WriteString("|---|---|---|---|\n")

	for _, cat := range categories {
		v := dues[cat]
		priority, limit := v[0], v[1]
		effectiveLimit := limit
		if priority > 1 {
			effectiveLimit *= plotCount
		}
		paid := outstanding[cat]
		remaining := float64(effectiveLimit) - paid
		if remaining < 0 {
			remaining = 0
		}
		sb.WriteString(fmt.Sprintf("| %s | %d | %.0f | %.0f |\n", cat, effectiveLimit, paid, remaining))
	}

	return sb.String()
}

func (b *Bot) handleReady(chatID, userID int64, fields ai.Fields) {
	vf, errMsg := b.validateFields(fields)
	if errMsg != "" {
		st := b.states.Get(userID)
		st.RetryCount++
		if st.RetryCount >= 3 {
			b.states.Clear(userID)
			b.clearPending(userID)
			b.sendMenu(chatID, "Не удалось извлечь корректные данные. Попробуйте снова.")
			return
		}
		st.History = append(st.History, ai.Msg{
			Role:    "user",
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
	b.setPending(userID, &pendingConfirm{rows: rows, curBal: bal, createdAt: time.Now()})

	m := tgbotapi.NewMessage(chatID, formatPreview(rows, bal))
	m.ParseMode = "MarkdownV2"
	m.ReplyMarkup = confirmKeyboard()
	if _, err := b.api.Send(m); err != nil {
		log.Printf("send confirm user %d: %v", userID, err)
	}
}

func (b *Bot) buildRows(vf validatedFields, year int) ([]distribution.DistributionRow, error) {
	if vf.Direction == "приход" && vf.Membership != "-" {
		dues := b.cfg.DuesFor(vf.Membership)
		if _, ok := dues[vf.Category]; ok {
			return b.buildDistributionRows(vf, year)
		}
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

func (b *Bot) buildDistributionRows(vf validatedFields, year int) ([]distribution.DistributionRow, error) {
	dues := b.cfg.DuesFor(vf.Membership)
	categories := config.SortedCategories(dues)
	plotCount := b.cfg.PlotCount(vf.Plot)

	paidCur, err := db.GetOutstanding(b.db, vf.Plot, year)
	if err != nil {
		return nil, fmt.Errorf("get outstanding current year: %w", err)
	}
	paidNext, err := db.GetOutstanding(b.db, vf.Plot, year+1)
	if err != nil {
		return nil, fmt.Errorf("get outstanding next year: %w", err)
	}

	outstanding := make(map[string]float64, len(dues))
	nextYearDue := make(map[string]float64, len(dues))
	for _, cat := range categories {
		v := dues[cat]
		priority, limit := v[0], v[1]
		effectiveLimit := float64(limit)
		if priority > 1 {
			effectiveLimit *= float64(plotCount)
		}
		if rem := effectiveLimit - paidCur[cat]; rem > 0 {
			outstanding[cat] = rem
		}
		if rem := effectiveLimit - paidNext[cat]; rem > 0 {
			nextYearDue[cat] = rem
		}
	}

	fields := distribution.OperationFields{
		Date:        vf.Date,
		Direction:   vf.Direction,
		PaymentType: vf.PaymentType,
		Plot:        vf.Plot,
		Category:    vf.Category,
		Note:        vf.Note,
		Membership:  vf.Membership,
		Amount:      vf.Amount,
	}

	return distribution.ComputeDistribution(fields, outstanding, categories, year, nextYearDue)
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil {
		return
	}
	chatID := cb.Message.Chat.ID
	userID := cb.From.ID
	log.Printf("[CALLBACK] userID=%d chatID=%d data=%q", userID, chatID, cb.Data)

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
		if time.Since(p.createdAt) > time.Duration(b.cfg.StateTimeoutMinutes)*time.Minute {
			b.clearPending(userID)
			b.send(chatID, "Операция устарела. Пожалуйста, введите заново.")
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

	b.states.Clear(userID)
	b.sendMenuMarkdown(chatID, formatBalanceMessage(bal, income, expense, ops))
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
	if f.Plot == nil || !contains(b.cfg.Plots(), *f.Plot) {
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
	log.Printf("[OUT] chatID=%d text=%q", chatID, text)
	if _, err := b.api.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		log.Printf("send chatID %d: %v", chatID, err)
	}
}

func (b *Bot) sendMenu(chatID int64, text string) {
	if text == "" {
		text = "Главное меню."
	}
	log.Printf("[OUT] chatID=%d text=%q", chatID, text)
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyMarkup = mainKeyboard()
	if _, err := b.api.Send(m); err != nil {
		log.Printf("send menu chatID %d: %v", chatID, err)
	}
}

func (b *Bot) sendMenuMarkdown(chatID int64, text string) {
	log.Printf("[OUT] chatID=%d text=%q", chatID, text)
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "MarkdownV2"
	m.ReplyMarkup = mainKeyboard()
	if _, err := b.api.Send(m); err != nil {
		log.Printf("send markdown menu chatID %d: %v", chatID, err)
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

func (b *Bot) clearBusy(userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.busy, userID)
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
	bal := curBal
	tableRows := make([][]string, 0, len(rows))
	for _, r := range rows {
		if r.Direction == "приход" {
			bal += r.Amount
		} else {
			bal -= r.Amount
		}
		tableRows = append(tableRows, []string{
			r.ContributionID,
			r.Direction,
			formatMoney(r.Amount),
			strconv.Itoa(r.FiscalYear),
			r.Membership,
			r.Plot,
			r.PaymentType,
			formatMoney(bal),
		})
	}

	var sb strings.Builder
	sb.WriteString(escapeMarkdownV2(fmt.Sprintf("📋 Предпросмотр (%d строк)", len(rows))))
	sb.WriteString("\n")
	sb.WriteString(formatMarkdownTable(
		[]string{"Категория", "Напр.", "Сумма", "Год", "Членство", "Участок", "Платеж", "Баланс после"},
		tableRows,
	))
	sb.WriteString("\n")
	sb.WriteString(escapeMarkdownV2(fmt.Sprintf("💳 Итоговый баланс: %s руб.", formatMoney(bal))))
	return sb.String()
}

func formatBalanceMessage(balance, income, expense float64, ops []db.OperationRow) string {
	tableRows := make([][]string, 0, len(ops))
	for _, op := range ops {
		tableRows = append(tableRows, []string{
			op.OpDate,
			op.Direction,
			formatMoney(op.Amount),
			op.Plot,
			op.Category,
			op.Membership,
			op.PaymentType,
			formatMoney(op.BalanceAfter),
		})
	}

	var sb strings.Builder
	sb.WriteString(escapeMarkdownV2(fmt.Sprintf("📊 Баланс: %s руб.", formatMoney(balance))))
	sb.WriteString("\n")
	sb.WriteString(escapeMarkdownV2(fmt.Sprintf("⬆️ Приход всего: %s руб.", formatMoney(income))))
	sb.WriteString("\n")
	sb.WriteString(escapeMarkdownV2(fmt.Sprintf("⬇️ Расход всего: %s руб.", formatMoney(expense))))
	sb.WriteString("\n\n")
	sb.WriteString(escapeMarkdownV2(fmt.Sprintf("🕐 Последние %d операций", len(ops))))
	sb.WriteString("\n")
	sb.WriteString(formatMarkdownTable(
		[]string{"Дата", "Напр.", "Сумма", "Участок", "Категория", "Членство", "Платеж", "Баланс после"},
		tableRows,
	))
	return sb.String()
}

func formatMarkdownTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len([]rune(h))
	}
	for _, row := range rows {
		for i := range headers {
			cell := ""
			if i < len(row) {
				cell = sanitizeTableCell(row[i])
			}
			if w := len([]rune(cell)); w > widths[i] {
				widths[i] = w
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("```\n")
	sb.WriteString(formatTableRow(headers, widths))
	sb.WriteString("\n")
	separator := make([]string, len(headers))
	for i, width := range widths {
		separator[i] = strings.Repeat("-", width)
	}
	sb.WriteString(formatTableRow(separator, widths))
	for _, row := range rows {
		sb.WriteString("\n")
		cells := make([]string, len(headers))
		for i := range headers {
			if i < len(row) {
				cells[i] = sanitizeTableCell(row[i])
			}
		}
		sb.WriteString(formatTableRow(cells, widths))
	}
	sb.WriteString("\n```")
	return sb.String()
}

func formatTableRow(cells []string, widths []int) string {
	var sb strings.Builder
	sb.WriteString("| ")
	for i, cell := range cells {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(padRight(cell, widths[i]))
	}
	sb.WriteString(" |")
	return sb.String()
}

func padRight(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(runes))
}

func sanitizeTableCell(s string) string {
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"|", "/",
		"`", "'",
		"\\", "/",
	)
	s = strings.TrimSpace(replacer.Replace(s))
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	if s == "" {
		return "-"
	}
	return s
}

func escapeMarkdownV2(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(s)
}

func formatMoney(v float64) string {
	return fmt.Sprintf("%.2f", v)
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
