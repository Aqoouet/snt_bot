package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	PlotMembershipMap              map[string]string  `json:"PLOT_MEMBERSHIP"`
	ContributionTypes              []ContributionType `json:"CONTRIBUTION_TYPES"`
	ContributionPriorityMember     []string           `json:"CONTRIBUTION_PRIORITY_MEMBER"`
	ContributionPriorityIndividual []string           `json:"CONTRIBUTION_PRIORITY_INDIVIDUAL"`
	ContributionAmounts            map[string]float64 `json:"CONTRIBUTION_AMOUNTS"`
	MaxPaymentAmount               float64            `json:"MAX_PAYMENT_AMOUNT"`
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
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.TelegramBotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if len(c.TelegramAllowedUserIDs) == 0 {
		return fmt.Errorf("TELEGRAM_ALLOWED_USER_IDS is required")
	}
	if c.OpenAIBaseURL == "" {
		return fmt.Errorf("OPENAI_BASE_URL is required")
	}
	if c.OpenAIModel == "" {
		return fmt.Errorf("OPENAI_MODEL is required")
	}
	if len(c.CategoriesIncome) == 0 {
		return fmt.Errorf("CATEGORIES_INCOME is required")
	}
	if len(c.CategoriesExpense) == 0 {
		return fmt.Errorf("CATEGORIES_EXPENSE is required")
	}
	if len(c.PaymentTypes) == 0 {
		return fmt.Errorf("PAYMENT_TYPES is required")
	}
	if len(c.PlotMembershipMap) == 0 {
		return fmt.Errorf("PLOT_MEMBERSHIP is required")
	}
	if len(c.ContributionAmounts) == 0 {
		return fmt.Errorf("CONTRIBUTION_AMOUNTS is required")
	}
	return nil
}

// Plots derives the full list of valid plot identifiers from PLOT_MEMBERSHIP keys.
// Keys may be comma-separated (e.g. "27,28") and are expanded to individual entries.
func (c *Config) Plots() []string {
	seen := make(map[string]struct{})
	var out []string
	for key := range c.PlotMembershipMap {
		for _, part := range strings.Split(key, ",") {
			p := strings.TrimSpace(part)
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	return out
}

// PlotMembership returns the membership type for a plot.
// Handles comma-separated keys such as "27,28".
func (c *Config) PlotMembership(plot string) string {
	if m, ok := c.PlotMembershipMap[plot]; ok {
		return m
	}
	for key, val := range c.PlotMembershipMap {
		for _, part := range strings.Split(key, ",") {
			if strings.TrimSpace(part) == plot {
				return val
			}
		}
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
