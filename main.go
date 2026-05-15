package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"snt-bot/internal/ai"
	"snt-bot/internal/bot"
	"snt-bot/internal/config"
	"snt-bot/internal/db"
	"snt-bot/internal/state"
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = ".env"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	sqlDB, err := db.Open(cfg.DBFile)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	promptPath := os.Getenv("PROMPT_PATH")
	if promptPath == "" {
		promptPath = "prompts/extraction_agent.md"
	}
	promptTpl, err := os.ReadFile(promptPath)
	if err != nil {
		log.Fatalf("read prompt: %v", err)
	}

	now := time.Now()
	today := now.Format("02.01.2006")
	yesterday := now.AddDate(0, 0, -1).Format("02.01.2006")

	sysPrompt := ai.BuildPrompt(
		string(promptTpl),
		cfg.PaymentTypes,
		cfg.Plots(),
		cfg.CategoriesIncome,
		cfg.CategoriesExpense,
		today,
		yesterday,
	)

	client := ai.NewClient(cfg.OpenAIBaseURL, cfg.OpenAIModel, cfg.OpenAIAPIKey, sysPrompt)
	states := state.NewManager(cfg.StateTimeoutMinutes)

	b, err := bot.New(cfg.TelegramBotToken, sqlDB, cfg, client, states)
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go b.Run()
	<-ctx.Done()
	b.Stop()
}
