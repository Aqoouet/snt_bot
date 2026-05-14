package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type ContributionType struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PayerType string `json:"payer_type"`
}

type Config struct {
	TelegramBotToken               string             `json:"TELEGRAM_BOT_TOKEN"`
	TelegramAllowedUserIDs         []int64            `json:"TELEGRAM_ALLOWED_USER_IDS"`
	InitialBalance                 float64            `json:"INITIAL_BALANCE"`
	OpenAIBaseURL                  string             `json:"OPENAI_BASE_URL"`
	OpenAIAPIKey                   string             `json:"OPENAI_API_KEY"`
	OpenAIModel                    string             `json:"OPENAI_MODEL"`
	DBFile                         string             `json:"DB_FILE"`
	StateTimeoutMinutes            int                `json:"STATE_TIMEOUT_MINUTES"`
	CategoriesIncome               []string           `json:"CATEGORIES_INCOME"`
	CategoriesExpense              []string           `json:"CATEGORIES_EXPENSE"`
	PaymentTypes                   []string           `json:"PAYMENT_TYPES"`
	Plots                          []string           `json:"PLOTS"`
	PlotMembershipMap              map[string]string  `json:"PLOT_MEMBERSHIP"`
	ContributionTypes              []ContributionType `json:"CONTRIBUTION_TYPES"`
	ContributionPriorityMember     []string           `json:"CONTRIBUTION_PRIORITY_MEMBER"`
	ContributionPriorityIndividual []string           `json:"CONTRIBUTION_PRIORITY_INDIVIDUAL"`
	ContributionAmounts            map[string]float64 `json:"CONTRIBUTION_AMOUNTS"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.DBFile == "" {
		cfg.DBFile = "snt.db"
	}
	if cfg.StateTimeoutMinutes == 0 {
		cfg.StateTimeoutMinutes = 5
	}
	return &cfg, nil
}

func (c *Config) PlotMembership(plot string) string {
	if m, ok := c.PlotMembershipMap[plot]; ok {
		return m
	}
	return "-"
}

func (c *Config) IsAllowedUser(userID int64) bool {
	for _, id := range c.TelegramAllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func (c *Config) PriorityFor(membership string) []string {
	if membership == "Индивидуал" {
		return c.ContributionPriorityIndividual
	}
	return c.ContributionPriorityMember
}
