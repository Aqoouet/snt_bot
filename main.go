package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken             string   `json:"bot_token"`
	AdminID              int64    `json:"admin_id"`
	StartingBalance      float64  `json:"starting_balance"`
	DBFile               string   `json:"db_file"`
	Categories           []string `json:"categories"`
	StateTimeoutMinutes  int      `json:"state_timeout_minutes"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Ошибка конфигурации: %v\n\n"+
			"Создайте config.json или задайте переменные окружения:\n"+
			"  BOT_TOKEN=...\n"+
			"  ADMIN_ID=...\n"+
			"  STARTING_BALANCE=0\n"+
			"  CATEGORIES=Электричество,Вода,Охрана,Прочее\n"+
			"  STATE_TIMEOUT_MINUTES=5\n"+
			"  DB_FILE=snt.db", err)
	}

	storage, err := NewStorage(cfg.DBFile, cfg.StartingBalance)
	if err != nil {
		log.Fatalf("Ошибка инициализации базы данных: %v", err)
	}

	timeout := time.Duration(cfg.StateTimeoutMinutes) * time.Minute
	bot := NewBot(cfg.BotToken)
	handler := NewHandler(bot, storage, cfg.AdminID, cfg.Categories, timeout)

	_, _, _, current := storage.GetBalance()
	log.Printf("SNT Bot запущен. Текущий баланс: %.2f ₽", current)
	log.Printf("База данных: %s", cfg.DBFile)
	log.Printf("Admin ID: %d", cfg.AdminID)
	log.Printf("Категории: %s", strings.Join(cfg.Categories, ", "))
	log.Printf("Таймаут состояния: %d мин", cfg.StateTimeoutMinutes)

	bot.SendMessage(cfg.AdminID, fmt.Sprintf(
		"🤖 <b>Бот учёта баланса СНТ запущен</b>\n\n"+
			"💼 Текущий баланс: <b>%.2f ₽</b>\n"+
			"🏷 Категории: %s\n\n"+
			"Используйте /help для списка команд.",
		current, escapeHTML(strings.Join(cfg.Categories, ", "))))

	bot.Poll(handler.Handle)
}

func loadConfig() (Config, error) {
	cfg := Config{
		DBFile:              "snt.db",
		StateTimeoutMinutes: 5,
	}

	if f, err := os.Open("config.json"); err == nil {
		defer f.Close()
		if err := json.NewDecoder(f).Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("ошибка чтения config.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("ошибка открытия config.json: %w", err)
	}

	if v := os.Getenv("BOT_TOKEN"); v != "" {
		cfg.BotToken = v
	}
	if v := os.Getenv("ADMIN_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("ADMIN_ID должен быть числом: %w", err)
		}
		cfg.AdminID = id
	}
	if v := os.Getenv("STARTING_BALANCE"); v != "" {
		bal, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return cfg, fmt.Errorf("STARTING_BALANCE должен быть числом: %w", err)
		}
		cfg.StartingBalance = bal
	}
	if v := os.Getenv("DB_FILE"); v != "" {
		cfg.DBFile = v
	}
	if v := os.Getenv("CATEGORIES"); v != "" {
		parts := strings.Split(v, ",")
		cfg.Categories = cfg.Categories[:0]
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				cfg.Categories = append(cfg.Categories, s)
			}
		}
	}
	if v := os.Getenv("STATE_TIMEOUT_MINUTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("STATE_TIMEOUT_MINUTES должен быть положительным числом")
		}
		cfg.StateTimeoutMinutes = n
	}

	if cfg.BotToken == "" {
		return cfg, fmt.Errorf("BOT_TOKEN не задан")
	}
	if cfg.AdminID == 0 {
		return cfg, fmt.Errorf("ADMIN_ID не задан")
	}
	if len(cfg.Categories) == 0 {
		return cfg, fmt.Errorf("CATEGORIES не заданы (пример: Электричество,Вода,Охрана,Прочее)")
	}
	return cfg, nil
}
