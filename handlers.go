package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type UserStep int

const (
	StepIdle UserStep = iota
	StepAwaitExpenseDesc
	StepAwaitExpenseCategory
	StepAwaitExpenseAmount
	StepAwaitIncomeDesc
	StepAwaitIncomeCategory
	StepAwaitIncomeAmount
)

type UserState struct {
	Step         UserStep
	Description  string
	Category     string
	ChatID       int64
	LastActivity time.Time
}

type Handler struct {
	bot     *Bot
	storage *Storage
	adminID int64

	categories   []string
	stateTimeout time.Duration

	mu     sync.Mutex
	states map[int64]*UserState

	pendingMu sync.Mutex
	pending   map[int64]string
}

func NewHandler(bot *Bot, storage *Storage, adminID int64, categories []string, stateTimeout time.Duration) *Handler {
	h := &Handler{
		bot:          bot,
		storage:      storage,
		adminID:      adminID,
		categories:   categories,
		stateTimeout: stateTimeout,
		states:       make(map[int64]*UserState),
		pending:      make(map[int64]string),
	}
	go h.runTimeoutWatcher()
	return h
}

// runTimeoutWatcher clears stale FSM states every 30 seconds.
func (h *Handler) runTimeoutWatcher() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		now := time.Now()
		h.mu.Lock()
		for uid, s := range h.states {
			if s.Step != StepIdle && now.Sub(s.LastActivity) > h.stateTimeout {
				chatID := s.ChatID
				delete(h.states, uid)
				go h.bot.SendMessage(chatID, "⏰ Время ожидания истекло. Действие отменено. Используйте /help для списка команд.")
			}
		}
		h.mu.Unlock()
	}
}

func (h *Handler) Handle(msg *TGMessage) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if text == "/start" {
		h.handleStart(chatID, userID)
		return
	}

	if !h.storage.IsAllowed(userID, h.adminID) {
		name := DisplayName(msg.From)
		h.notifyAdmin(userID, name)
		h.bot.SendMessage(chatID, "⛔ У вас нет доступа к боту.\n\nВаш запрос отправлен администратору.")
		return
	}

	// /cancel works from any state
	if text == "/cancel" {
		h.clearState(userID)
		h.bot.SendMessage(chatID, "✅ Действие отменено.")
		return
	}

	// Admin-only commands
	if userID == h.adminID {
		switch {
		case strings.HasPrefix(text, "/adduser"):
			h.handleAddUser(chatID, text)
			return
		case strings.HasPrefix(text, "/removeuser"):
			h.handleRemoveUser(chatID, text)
			return
		case text == "/users":
			h.handleListUsers(chatID)
			return
		case text == "/pending":
			h.handlePending(chatID)
			return
		}
	}

	// Named commands — always interrupt any active flow
	switch text {
	case "/expense":
		h.startFlow(chatID, userID, StepAwaitExpenseDesc)
		h.bot.SendMessage(chatID, "💸 <b>Расход</b>\n\nВведите описание (на что ушли деньги):")
		return
	case "/income":
		h.startFlow(chatID, userID, StepAwaitIncomeDesc)
		h.bot.SendMessage(chatID, "💰 <b>Приход</b>\n\nВведите описание (источник средств):")
		return
	case "/balance":
		h.handleBalance(chatID)
		return
	case "/export":
		h.handleExport(chatID)
		return
	case "/help":
		h.handleHelp(chatID, userID)
		return
	}

	// Multi-step FSM
	state := h.getOrCreateState(userID, chatID)
	state.LastActivity = time.Now()

	switch state.Step {
	case StepAwaitExpenseDesc:
		if text == "" {
			h.bot.SendMessage(chatID, "Описание не может быть пустым. Введите описание:")
			return
		}
		state.Description = text
		state.Step = StepAwaitExpenseCategory
		h.bot.SendMessage(chatID, fmt.Sprintf(
			"📝 Описание: <i>%s</i>\n\nВыберите категорию:\n%s",
			escapeHTML(text), h.categoryList()))

	case StepAwaitExpenseCategory:
		cat, ok := h.matchCategory(text)
		if !ok {
			h.bot.SendMessage(chatID, fmt.Sprintf(
				"❌ Неверная категория. Выберите из списка:\n%s", h.categoryList()))
			return
		}
		state.Category = cat
		state.Step = StepAwaitExpenseAmount
		h.bot.SendMessage(chatID, fmt.Sprintf(
			"🏷 Категория: <i>%s</i>\n\nВведите сумму расхода (например: 1500.50):", escapeHTML(cat)))

	case StepAwaitExpenseAmount:
		amount, err := parseAmount(text)
		if err != nil {
			h.bot.SendMessage(chatID, "❌ Неверный формат суммы. Введите число (например: 1500 или 1500.50):")
			return
		}
		h.saveTransaction(chatID, msg.From, "expense", state.Description, state.Category, amount)
		h.clearState(userID)

	case StepAwaitIncomeDesc:
		if text == "" {
			h.bot.SendMessage(chatID, "Описание не может быть пустым. Введите описание:")
			return
		}
		state.Description = text
		state.Step = StepAwaitIncomeCategory
		h.bot.SendMessage(chatID, fmt.Sprintf(
			"📝 Описание: <i>%s</i>\n\nВыберите категорию:\n%s",
			escapeHTML(text), h.categoryList()))

	case StepAwaitIncomeCategory:
		cat, ok := h.matchCategory(text)
		if !ok {
			h.bot.SendMessage(chatID, fmt.Sprintf(
				"❌ Неверная категория. Выберите из списка:\n%s", h.categoryList()))
			return
		}
		state.Category = cat
		state.Step = StepAwaitIncomeAmount
		h.bot.SendMessage(chatID, fmt.Sprintf(
			"🏷 Категория: <i>%s</i>\n\nВведите сумму прихода (например: 5000):", escapeHTML(cat)))

	case StepAwaitIncomeAmount:
		amount, err := parseAmount(text)
		if err != nil {
			h.bot.SendMessage(chatID, "❌ Неверный формат суммы. Введите число (например: 5000 или 5000.50):")
			return
		}
		h.saveTransaction(chatID, msg.From, "income", state.Description, state.Category, amount)
		h.clearState(userID)

	default:
		h.handleHelp(chatID, userID)
	}
}

// ---- Command handlers ----

func (h *Handler) handleStart(chatID, userID int64) {
	if h.storage.IsAllowed(userID, h.adminID) {
		_, income, expense, current := h.storage.GetBalance()
		h.bot.SendMessage(chatID, fmt.Sprintf(
			"👋 <b>Добро пожаловать в бот учёта баланса СНТ!</b>\n\n"+
				"💼 Текущий баланс: <b>%.2f ₽</b>\n"+
				"📈 Приход: %.2f ₽ | 📉 Расход: %.2f ₽\n\n"+
				"Используйте /help для списка команд.",
			current, income, expense))
	} else {
		h.bot.SendMessage(chatID, "👋 Добро пожаловать!\n\nНапишите любое сообщение — запрос на доступ будет отправлен администратору.")
	}
}

func (h *Handler) handleHelp(chatID int64, userID int64) {
	text := "📋 <b>Доступные команды:</b>\n\n" +
		"/expense — зарегистрировать расход\n" +
		"/income — зарегистрировать приход\n" +
		"/balance — текущий баланс\n" +
		"/export — выгрузка в Excel\n" +
		"/cancel — отменить текущее действие\n"
	if userID == h.adminID {
		text += "\n<b>Команды администратора:</b>\n" +
			"/adduser &lt;id&gt; — добавить пользователя\n" +
			"/removeuser &lt;id&gt; — удалить пользователя\n" +
			"/users — список авторизованных пользователей\n" +
			"/pending — запросы на доступ\n"
	}
	h.bot.SendMessage(chatID, text)
}

func (h *Handler) handleBalance(chatID int64) {
	starting, income, expense, current := h.storage.GetBalance()
	h.bot.SendMessage(chatID, fmt.Sprintf(
		"💼 <b>Баланс СНТ</b>\n\n"+
			"🏦 Начальный баланс: <b>%.2f ₽</b>\n"+
			"📈 Итого приход: <b>+%.2f ₽</b>\n"+
			"📉 Итого расход: <b>-%.2f ₽</b>\n\n"+
			"💰 <b>Текущий баланс: %.2f ₽</b>",
		starting, income, expense, current))
}

func (h *Handler) handleExport(chatID int64) {
	h.bot.SendMessage(chatID, "⏳ Формирую файл...")
	transactions := h.storage.GetTransactions()
	startingBalance := h.storage.GetStartingBalance()

	data, err := GenerateXLSX(transactions, startingBalance)
	if err != nil {
		h.bot.SendMessage(chatID, "❌ Ошибка при создании файла: "+err.Error())
		return
	}

	filename := fmt.Sprintf("snt_balance_%s.xlsx", time.Now().Format("2006-01-02"))
	if err := h.bot.SendDocument(chatID, filename, data); err != nil {
		h.bot.SendMessage(chatID, "❌ Ошибка при отправке файла: "+err.Error())
	}
}

func (h *Handler) handleAddUser(chatID int64, text string) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		h.bot.SendMessage(chatID, "Использование: /adduser &lt;user_id&gt;\n\nНайдите ID в /pending")
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		h.bot.SendMessage(chatID, "❌ Неверный ID пользователя.")
		return
	}

	h.pendingMu.Lock()
	name, ok := h.pending[id]
	if ok {
		delete(h.pending, id)
	}
	h.pendingMu.Unlock()

	if !ok {
		name = fmt.Sprintf("ID:%d", id)
	}

	if err := h.storage.AddUser(id, name); err != nil {
		h.bot.SendMessage(chatID, "❌ Ошибка при добавлении: "+err.Error())
		return
	}
	h.bot.SendMessage(chatID, fmt.Sprintf("✅ Пользователь <b>%s</b> добавлен.", escapeHTML(name)))
	h.bot.SendMessage(id, "✅ Вам выдан доступ к боту учёта баланса СНТ!\n\nИспользуйте /help для списка команд.")
}

func (h *Handler) handleRemoveUser(chatID int64, text string) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		h.bot.SendMessage(chatID, "Использование: /removeuser &lt;user_id&gt;")
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		h.bot.SendMessage(chatID, "❌ Неверный ID пользователя.")
		return
	}
	if err := h.storage.RemoveUser(id); err != nil {
		h.bot.SendMessage(chatID, "❌ Ошибка: "+err.Error())
		return
	}
	h.bot.SendMessage(chatID, fmt.Sprintf("✅ Пользователь ID %d удалён.", id))
}

func (h *Handler) handleListUsers(chatID int64) {
	users := h.storage.ListUsers()
	if len(users) == 0 {
		h.bot.SendMessage(chatID, "Список авторизованных пользователей пуст.")
		return
	}
	var sb strings.Builder
	sb.WriteString("👥 <b>Авторизованные пользователи:</b>\n\n")
	for id, name := range users {
		sb.WriteString(fmt.Sprintf("• %s — ID: <code>%d</code>\n", escapeHTML(name), id))
	}
	h.bot.SendMessage(chatID, sb.String())
}

func (h *Handler) handlePending(chatID int64) {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	if len(h.pending) == 0 {
		h.bot.SendMessage(chatID, "Нет ожидающих запросов на доступ.")
		return
	}
	var sb strings.Builder
	sb.WriteString("⏳ <b>Запросы на доступ:</b>\n\n")
	for id, name := range h.pending {
		sb.WriteString(fmt.Sprintf("• %s — ID: <code>%d</code>\n  /adduser %d\n\n", escapeHTML(name), id, id))
	}
	h.bot.SendMessage(chatID, sb.String())
}

func (h *Handler) saveTransaction(chatID int64, from *TGUser, txType, description, category string, amount float64) {
	tx := Transaction{
		Type:        txType,
		Description: description,
		Category:    category,
		Amount:      amount,
		Date:        time.Now(),
		UserID:      from.ID,
		Username:    DisplayName(from),
	}
	if err := h.storage.AddTransaction(tx); err != nil {
		h.bot.SendMessage(chatID, "❌ Ошибка при сохранении: "+err.Error())
		return
	}

	_, _, _, current := h.storage.GetBalance()
	typeLabel := "Расход"
	typeIcon := "📉"
	if txType == "income" {
		typeLabel = "Приход"
		typeIcon = "📈"
	}
	h.bot.SendMessage(chatID, fmt.Sprintf(
		"✅ <b>%s зарегистрирован</b>\n\n"+
			"%s Тип: %s\n"+
			"🏷 Категория: %s\n"+
			"📝 Описание: %s\n"+
			"💵 Сумма: <b>%.2f ₽</b>\n\n"+
			"💼 Текущий баланс: <b>%.2f ₽</b>",
		typeLabel, typeIcon, typeLabel,
		escapeHTML(category), escapeHTML(description), amount, current))
}

func (h *Handler) notifyAdmin(userID int64, name string) {
	h.pendingMu.Lock()
	_, alreadyPending := h.pending[userID]
	if !alreadyPending {
		h.pending[userID] = name
	}
	h.pendingMu.Unlock()

	if !alreadyPending {
		h.bot.SendMessage(h.adminID, fmt.Sprintf(
			"🔔 <b>Запрос на доступ</b>\n\n"+
				"Пользователь: %s\n"+
				"ID: <code>%d</code>\n\n"+
				"Для выдачи доступа: /adduser %d",
			escapeHTML(name), userID, userID))
	}
}

// ---- FSM helpers ----

func (h *Handler) getOrCreateState(userID, chatID int64) *UserState {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.states[userID]; ok {
		s.ChatID = chatID
		return s
	}
	s := &UserState{ChatID: chatID, LastActivity: time.Now()}
	h.states[userID] = s
	return s
}

func (h *Handler) startFlow(chatID, userID int64, step UserStep) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.states[userID] = &UserState{Step: step, ChatID: chatID, LastActivity: time.Now()}
}

func (h *Handler) clearState(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.states, userID)
}

// ---- Category helpers ----

func (h *Handler) matchCategory(input string) (string, bool) {
	input = strings.TrimSpace(input)
	for _, c := range h.categories {
		if strings.EqualFold(input, c) {
			return c, true
		}
	}
	return "", false
}

func (h *Handler) categoryList() string {
	var sb strings.Builder
	for i, c := range h.categories {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
	}
	return sb.String()
}

// ---- Misc helpers ----

func parseAmount(s string) (float64, error) {
	s = strings.ReplaceAll(s, ",", ".")
	s = strings.ReplaceAll(s, " ", "")
	return strconv.ParseFloat(s, 64)
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
