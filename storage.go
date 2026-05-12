package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Transaction struct {
	ID          int64
	Type        string // "income" | "expense"
	Description string
	Category    string
	Amount      float64
	Date        time.Time
	UserID      int64
	Username    string
}

type Storage struct {
	db *sql.DB
}

func NewStorage(dbPath string, startingBalance float64) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// Single writer connection is enough for this bot
	db.SetMaxOpenConns(1)

	s := &Storage{db: db}
	if err := s.migrate(startingBalance); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Storage) migrate(startingBalance float64) error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS users (
			id       INTEGER PRIMARY KEY,
			username TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS transactions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			type        TEXT NOT NULL,
			description TEXT NOT NULL,
			category    TEXT NOT NULL DEFAULT '',
			amount      REAL NOT NULL,
			date        DATETIME NOT NULL,
			user_id     INTEGER NOT NULL,
			username    TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	// Set starting_balance only on first run (INSERT OR IGNORE)
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO config (key, value) VALUES ('starting_balance', ?)`,
		fmt.Sprintf("%f", startingBalance),
	)
	return err
}

func (s *Storage) IsAllowed(userID int64, adminID int64) bool {
	if userID == adminID {
		return true
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&count)
	return count > 0
}

func (s *Storage) AddUser(userID int64, username string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO users (id, username) VALUES (?, ?)`,
		userID, username,
	)
	return err
}

func (s *Storage) RemoveUser(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

func (s *Storage) ListUsers() map[int64]string {
	rows, err := s.db.Query(`SELECT id, username FROM users`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		if rows.Scan(&id, &name) == nil {
			result[id] = name
		}
	}
	return result
}

func (s *Storage) AddTransaction(tx Transaction) error {
	_, err := s.db.Exec(
		`INSERT INTO transactions (type, description, category, amount, date, user_id, username)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tx.Type, tx.Description, tx.Category, tx.Amount,
		tx.Date.UTC().Format(time.RFC3339), tx.UserID, tx.Username,
	)
	return err
}

func (s *Storage) GetBalance() (starting, income, expense, current float64) {
	var raw string
	s.db.QueryRow(`SELECT value FROM config WHERE key = 'starting_balance'`).Scan(&raw)
	fmt.Sscanf(raw, "%f", &starting)

	s.db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM transactions WHERE type = 'income'`).Scan(&income)
	s.db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM transactions WHERE type = 'expense'`).Scan(&expense)
	current = starting + income - expense
	return
}

func (s *Storage) GetTransactions() []Transaction {
	rows, err := s.db.Query(
		`SELECT id, type, description, category, amount, date, user_id, username
		 FROM transactions ORDER BY id`,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []Transaction
	for rows.Next() {
		var tx Transaction
		var dateStr string
		if err := rows.Scan(&tx.ID, &tx.Type, &tx.Description, &tx.Category,
			&tx.Amount, &dateStr, &tx.UserID, &tx.Username); err != nil {
			continue
		}
		tx.Date, _ = time.Parse(time.RFC3339, dateStr)
		result = append(result, tx)
	}
	return result
}

func (s *Storage) GetStartingBalance() float64 {
	var raw string
	s.db.QueryRow(`SELECT value FROM config WHERE key = 'starting_balance'`).Scan(&raw)
	var v float64
	fmt.Sscanf(raw, "%f", &v)
	return v
}
