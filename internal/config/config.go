package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type Config struct {
	TelegramBotToken       string            `json:"TELEGRAM_BOT_TOKEN"`
	TelegramAllowedUserIDs []int64           `json:"TELEGRAM_ALLOWED_USER_IDS"`
	InitialBalance         float64           `json:"INITIAL_BALANCE"`
	OpenAIBaseURL          string            `json:"OPENAI_BASE_URL"`
	OpenAIAPIKey           string            `json:"OPENAI_API_KEY"`
	OpenAIModel            string            `json:"OPENAI_MODEL"`
	DBFile                 string            `json:"DB_FILE"`
	StateTimeoutMinutes    int               `json:"STATE_TIMEOUT_MINUTES"`
	CategoriesIncome       []string          `json:"CATEGORIES_INCOME"`
	CategoriesExpense      []string          `json:"CATEGORIES_EXPENSE"`
	PaymentTypes           []string          `json:"PAYMENT_TYPES"`
	PlotMembershipMap      map[string]string `json:"PLOT_MEMBERSHIP"`
	CategoriesIncomeIndiv  map[string][2]int `json:"CATEGORIES_INCOME_INDIV"`
	CategoriesIncomeMember map[string][2]int `json:"CATEGORIES_INCOME_MEMBER"`
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
	if len(c.CategoriesIncomeIndiv) == 0 {
		return fmt.Errorf("CATEGORIES_INCOME_INDIV is required")
	}
	if len(c.CategoriesIncomeMember) == 0 {
		return fmt.Errorf("CATEGORIES_INCOME_MEMBER is required")
	}
	return nil
}

// Plots returns all valid plot IDs from PLOT_MEMBERSHIP keys.
// Composite keys like "27,28" are kept as-is AND expanded to individual parts.
func (c *Config) Plots() []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(p string) {
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	for key := range c.PlotMembershipMap {
		if strings.Contains(key, ",") {
			add(key)
		}
		for _, part := range strings.Split(key, ",") {
			add(strings.TrimSpace(part))
		}
	}
	return out
}

// PlotMembership returns membership type for a plot.
// Handles composite keys like "27,28".
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

// DuesFor returns dues map for a membership type.
// [2]int = [priority, annual_limit_rub]
func (c *Config) DuesFor(membership string) map[string][2]int {
	if membership == "Индивидуал" {
		return c.CategoriesIncomeIndiv
	}
	return c.CategoriesIncomeMember
}

// PlotCount returns number of plots in a comma-separated plot string.
func (c *Config) PlotCount(plot string) int {
	return len(strings.Split(plot, ","))
}

// SortedCategories returns category names sorted by priority (v[0]) ascending.
func SortedCategories(dues map[string][2]int) []string {
	type entry struct {
		name     string
		priority int
	}
	entries := make([]entry, 0, len(dues))
	for name, v := range dues {
		entries = append(entries, entry{name, v[0]})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].priority < entries[j].priority
	})
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.name
	}
	return out
}
