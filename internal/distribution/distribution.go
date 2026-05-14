package distribution

import (
	"database/sql"
	"fmt"
	"math"

	"github.com/google/uuid"
	"snt-bot/internal/db"
)

type OperationFields struct {
	Date        string
	Direction   string
	PaymentType string
	Plot        string
	Category    string
	Note        string
	Membership  string
	Amount      float64
}

type DistributionRow struct {
	ContributionID string
	Membership     string
	Plot           string
	PaymentType    string
	OpDate         string
	Category       string
	Note           string
	Direction      string
	Amount         float64
	FiscalYear     int
	BalanceAfter   float64
}

// ComputeDistribution is a pure function — no DB writes.
// outstanding: remaining debt per contribution ID for current year (annualDue - alreadyPaid).
// nextYearDue: full annual amounts per contribution ID for next-year overflow.
// Returns one DistributionRow per allocation bucket. BalanceAfter is left at 0 (set by CommitDistribution).
func ComputeDistribution(
	fields OperationFields,
	outstanding map[string]float64,
	priorities []string,
	currentYear int,
	nextYearDue map[string]float64,
) ([]DistributionRow, error) {
	if fields.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}

	remaining := fields.Amount
	var rows []DistributionRow

	// Phase 1: fill current-year buckets in priority order.
	for _, id := range priorities {
		if remaining <= 0 {
			break
		}
		debt := outstanding[id]
		if debt <= 0 {
			continue
		}
		alloc := math.Min(remaining, debt)
		rows = append(rows, DistributionRow{
			ContributionID: id,
			Membership:     fields.Membership,
			Plot:           fields.Plot,
			PaymentType:    fields.PaymentType,
			OpDate:         fields.Date,
			Category:       id,
			Note:           fields.Note,
			Direction:      "приход",
			Amount:         round2(alloc),
			FiscalYear:     currentYear,
		})
		remaining = round2(remaining - alloc)
	}

	// Phase 2: overflow into next year in the same priority order.
	if remaining > 0 && len(nextYearDue) > 0 {
		for _, id := range priorities {
			if remaining <= 0 {
				break
			}
			due := nextYearDue[id]
			if due <= 0 {
				continue
			}
			alloc := math.Min(remaining, due)
			rows = append(rows, DistributionRow{
				ContributionID: id,
				Membership:     fields.Membership,
				Plot:           fields.Plot,
				PaymentType:    fields.PaymentType,
				OpDate:         fields.Date,
				Category:       id,
				Note:           fields.Note,
				Direction:      "приход",
				Amount:         round2(alloc),
				FiscalYear:     currentYear + 1,
			})
			remaining = round2(remaining - alloc)
		}
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("no outstanding debt to allocate")
	}
	return rows, nil
}

// CommitDistribution writes rows transactionally. Reads current balance inside
// the transaction. Uses initialBalance when the DB has no prior rows.
func CommitDistribution(sqlDB *sql.DB, rows []DistributionRow, initialBalance float64) error {
	tx, err := sqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Read balance inside the transaction for accuracy.
	var bal sql.NullFloat64
	err = tx.QueryRow(`SELECT balance_after FROM operations ORDER BY id DESC LIMIT 1`).Scan(&bal)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read balance: %w", err)
	}

	var runningBalance float64
	if err == sql.ErrNoRows || !bal.Valid {
		runningBalance = initialBalance
	} else {
		runningBalance = bal.Float64
	}

	groupID := uuid.New().String()

	for i := range rows {
		rows[i].BalanceAfter = round2(runningBalance + rows[i].Amount)
		runningBalance = rows[i].BalanceAfter

		row := db.OperationRow{
			Membership:     rows[i].Membership,
			OpDate:         rows[i].OpDate,
			Direction:      rows[i].Direction,
			Amount:         rows[i].Amount,
			PaymentType:    rows[i].PaymentType,
			Plot:           rows[i].Plot,
			FiscalYear:     rows[i].FiscalYear,
			Category:       rows[i].Category,
			Note:           rows[i].Note,
			BalanceAfter:   rows[i].BalanceAfter,
			PaymentGroupID: groupID,
		}
		if err := db.InsertOperation(tx, row); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
