package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS operations (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at       TIMESTAMP NOT NULL DEFAULT (datetime('now')),
	membership       TEXT NOT NULL,
	op_date          TEXT NOT NULL,
	direction        TEXT NOT NULL,
	amount           REAL NOT NULL,
	payment_type     TEXT NOT NULL,
	plot             TEXT NOT NULL,
	fiscal_year      INTEGER NOT NULL,
	category         TEXT NOT NULL,
	note             TEXT NOT NULL DEFAULT '',
	balance_after    REAL NOT NULL,
	payment_group_id TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_op_date    ON operations(op_date);
CREATE INDEX IF NOT EXISTS idx_created_at ON operations(created_at);
CREATE INDEX IF NOT EXISTS idx_plot_year  ON operations(plot, fiscal_year);
`

type OperationRow struct {
	ID             int64
	CreatedAt      time.Time
	Membership     string
	OpDate         string
	Direction      string
	Amount         float64
	PaymentType    string
	Plot           string
	FiscalYear     int
	Category       string
	Note           string
	BalanceAfter   float64
	PaymentGroupID string
}

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return db, nil
}

func GetBalance(db *sql.DB) (float64, error) {
	var bal sql.NullFloat64
	err := db.QueryRow(`SELECT balance_after FROM operations ORDER BY id DESC LIMIT 1`).Scan(&bal)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get balance: %w", err)
	}
	return bal.Float64, nil
}

// GetOutstanding returns total already paid per contribution_id for plot+year.
func GetOutstanding(db *sql.DB, plot string, fiscalYear int) (map[string]float64, error) {
	rows, err := db.Query(
		`SELECT category, SUM(amount) FROM operations WHERE plot=? AND fiscal_year=? AND direction='приход' GROUP BY category`,
		plot, fiscalYear,
	)
	if err != nil {
		return nil, fmt.Errorf("get outstanding: %w", err)
	}
	defer rows.Close()
	result := make(map[string]float64)
	for rows.Next() {
		var cat string
		var sum float64
		if err := rows.Scan(&cat, &sum); err != nil {
			return nil, err
		}
		result[cat] = sum
	}
	return result, rows.Err()
}

func InsertOperation(tx *sql.Tx, row OperationRow) error {
	_, err := tx.Exec(
		`INSERT INTO operations
		(membership, op_date, direction, amount, payment_type, plot, fiscal_year, category, note, balance_after, payment_group_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		row.Membership, row.OpDate, row.Direction, row.Amount,
		row.PaymentType, row.Plot, row.FiscalYear, row.Category,
		row.Note, row.BalanceAfter, row.PaymentGroupID,
	)
	if err != nil {
		return fmt.Errorf("insert operation: %w", err)
	}
	return nil
}

func LastNOperations(db *sql.DB, n int) ([]OperationRow, error) {
	rows, err := db.Query(
		`SELECT id, created_at, membership, op_date, direction, amount, payment_type, plot, fiscal_year, category, note, balance_after, payment_group_id
		 FROM operations ORDER BY id DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, fmt.Errorf("last n ops: %w", err)
	}
	return scanRows(rows)
}

func LastNRowsAsc(db *sql.DB, n int) ([]OperationRow, error) {
	rows, err := db.Query(
		`SELECT id, created_at, membership, op_date, direction, amount, payment_type, plot, fiscal_year, category, note, balance_after, payment_group_id
		 FROM (SELECT * FROM operations ORDER BY id DESC LIMIT ?) ORDER BY id ASC`, n,
	)
	if err != nil {
		return nil, fmt.Errorf("last n rows asc: %w", err)
	}
	return scanRows(rows)
}

func TotalCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&n)
	return n, err
}

func GetTotals(db *sql.DB) (income, expense float64, err error) {
	err = db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM operations WHERE direction='приход'`).Scan(&income)
	if err != nil {
		return
	}
	err = db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM operations WHERE direction='расход'`).Scan(&expense)
	return
}

func scanRows(rows *sql.Rows) ([]OperationRow, error) {
	defer rows.Close()
	var result []OperationRow
	for rows.Next() {
		var r OperationRow
		var createdAt string
		if err := rows.Scan(
			&r.ID, &createdAt, &r.Membership, &r.OpDate, &r.Direction,
			&r.Amount, &r.PaymentType, &r.Plot, &r.FiscalYear, &r.Category,
			&r.Note, &r.BalanceAfter, &r.PaymentGroupID,
		); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		result = append(result, r)
	}
	return result, rows.Err()
}
