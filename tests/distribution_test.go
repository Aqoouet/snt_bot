//go:build ignore

// distribution_test.go — 20 table-driven cases for the Deterministic Go Layer.
//
// SPEC v2 §18: "20 cases, identical to SPEC v1 §14. These test the Deterministic
// Go Layer only, independent of AI."
//
// Prerequisites before enabling this file (remove the //go:build ignore tag):
//   1. Create package sntbot (or internal/distribution) with ComputeDistribution.
//   2. Update the import path below to match the actual module path.
//   3. Replace t.Skip calls with real assertions.
//
// Expected signatures (subject to final API design):
//
//	func ComputeDistribution(fields OperationFields, outstanding map[string]float64, priorities []string) ([]DistributionRow, error)
//
// Test fixtures are loaded from ../.env TEST_FIXTURES to stay in sync with the spec.

package distribution_test

import (
	"encoding/json"
	"os"
	"testing"
)

// ---- stub types (replace with imports once the package exists) ----

type OperationFields struct {
	Date        string
	Direction   string
	Amount      float64
	PaymentType string
	Plot        string
	Category    string
	Note        string
}

type DistributionRow struct {
	ContributionID string
	Amount         float64
	FiscalYear     int
	Membership     string
	Plot           string
	PaymentType    string
	OpDate         string
	Category       string
	Note           string
}

// ComputeDistribution is a stub; replace with the real import.
func ComputeDistribution(_ OperationFields, _ map[string]float64, _ []string) ([]DistributionRow, error) {
	return nil, nil
}

// ---- fixture types ----

type testFixtures struct {
	ExampleDueMember     map[string]float64 `json:"EXAMPLE_DUE_MEMBER"`
	ExampleDueIndividual map[string]float64 `json:"EXAMPLE_DUE_INDIVIDUAL"`
	NextYearDueMember    map[string]float64 `json:"NEXT_YEAR_DUE_MEMBER"`
	NextYearDueIndividual map[string]float64 `json:"NEXT_YEAR_DUE_INDIVIDUAL"`
	Payments             map[string]interface{} `json:"PAYMENTS"`
}

var fixtures testFixtures

func TestMain(m *testing.M) {
	envRaw, err := os.ReadFile("../.env")
	if err != nil {
		panic("cannot read .env: " + err.Error())
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(envRaw, &env); err != nil {
		panic("cannot parse .env: " + err.Error())
	}
	if raw, ok := env["TEST_FIXTURES"]; ok {
		if err := json.Unmarshal(raw, &fixtures); err != nil {
			panic("cannot parse TEST_FIXTURES: " + err.Error())
		}
	}
	os.Exit(m.Run())
}

// paymentFloat extracts a single float64 from the PAYMENTS map.
func paymentFloat(key string) float64 {
	v, ok := fixtures.Payments[key]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return f
}

// memberPriority is the waterfall order for Член СНТ (SPEC §5.3).
var memberPriority = []string{"MEMBER_REGULAR", "TARGET_ROAD", "TARGET_FLOOD", "TARGET_DITCH"}

// individualPriority is the waterfall order for Садовод-индивидуал.
var individualPriority = []string{"INDIV_CURRENT", "TARGET_ROAD", "TARGET_FLOOD", "TARGET_DITCH"}

// memberPlot is a Член plot; individualPlot is an Индивидуал plot (from .env PLOT_MEMBERSHIP).
const (
	memberPlot     = "5"  // "Член"
	individualPlot = "15" // "Индивидуал"
)

// ---- 20 distribution cases ----

// Case structure: input payment, outstanding debts, priority, expected rows.
type distCase struct {
	name          string
	amount        float64
	outstanding   map[string]float64 // full annual debt (assume no prior payments)
	priority      []string
	wantRows      []DistributionRow // nil means: skip until implemented
	wantRowCount  int
}

// TestComputeDistribution_Member covers cases 1–10 for Член СНТ payers.
func TestComputeDistribution_Member(t *testing.T) {
	t.Skip("ComputeDistribution not yet implemented — remove skip once package exists")

	due := fixtures.ExampleDueMember // {MEMBER_REGULAR:8000, TARGET_ROAD:5000, TARGET_FLOOD:3000, TARGET_DITCH:2050}

	cases := []distCase{
		{
			// Case 1: small partial payment fills only first priority bucket.
			name:         "case01_partial_first_bucket_1000",
			amount:       paymentFloat("PAY_1000"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 1,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 1000},
			},
		},
		{
			// Case 2: payment exactly covers first priority bucket.
			name:         "case02_exact_member_regular_8000",
			amount:       paymentFloat("MEMBER_ONLY_8000"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 1,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
			},
		},
		{
			// Case 3: payment fills first bucket and spills into second.
			name:         "case03_fills_member_regular_plus_partial_road_10000",
			amount:       paymentFloat("PAY_10000"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 2,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 2000},
			},
		},
		{
			// Case 4: payment fills first two priority buckets exactly.
			name:         "case04_member_plus_road_13000",
			amount:       paymentFloat("MEMBER_PLUS_ROAD_13000"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 2,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
			},
		},
		{
			// Case 5: payment fills three buckets, last partially.
			name:         "case05_fills_three_partial_flood_15000",
			amount:       paymentFloat("PAY_15000"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 3,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
				{ContributionID: "TARGET_FLOOD", Amount: 2000},
			},
		},
		{
			// Case 6: payment almost covers full year — last bucket partial.
			name:         "case06_almost_full_18000",
			amount:       paymentFloat("ALMOST_TOTAL_18000"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 4,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
				{ContributionID: "TARGET_FLOOD", Amount: 3000},
				{ContributionID: "TARGET_DITCH", Amount: 2000},
			},
		},
		{
			// Case 7: payment exactly covers the full annual obligation.
			name:         "case07_exact_full_18050",
			amount:       paymentFloat("FULL_TOTAL_18050"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 4,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
				{ContributionID: "TARGET_FLOOD", Amount: 3000},
				{ContributionID: "TARGET_DITCH", Amount: 2050},
			},
		},
		{
			// Case 8: overpayment advances into next fiscal year (SPEC §5.3 last bullet).
			// 18100 = 18050 current year + 50 advance → one extra row next_year MEMBER_REGULAR=50.
			name:         "case08_overpay_advances_next_year_18100",
			amount:       paymentFloat("OVERPAY_18100"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 5,
			wantRows: []DistributionRow{
				{ContributionID: "MEMBER_REGULAR", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
				{ContributionID: "TARGET_FLOOD", Amount: 3000},
				{ContributionID: "TARGET_DITCH", Amount: 2050},
				{ContributionID: "MEMBER_REGULAR", Amount: 50}, // FiscalYear = currentYear+1
			},
		},
		{
			// Case 9: partial payment where only the last-priority bucket (TARGET_DITCH) is owed.
			// Simulates a payer who has already paid all other buckets.
			name:        "case09_only_last_bucket_due_2050",
			amount:      paymentFloat("PAY_2050"),
			outstanding: map[string]float64{"TARGET_DITCH": 2050},
			priority:    memberPriority,
			wantRowCount: 1,
			wantRows: []DistributionRow{
				{ContributionID: "TARGET_DITCH", Amount: 2050},
			},
		},
		{
			// Case 10: zero payment — should produce no rows (not an error).
			name:         "case10_zero_payment",
			amount:       paymentFloat("PAY_0"),
			outstanding:  due,
			priority:     memberPriority,
			wantRowCount: 0,
			wantRows:     []DistributionRow{},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fields := OperationFields{
				Amount: tc.amount,
				Plot:   memberPlot,
			}
			rows, err := ComputeDistribution(fields, tc.outstanding, tc.priority)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rows) != tc.wantRowCount {
				t.Errorf("row count: got %d, want %d", len(rows), tc.wantRowCount)
			}
			for i, want := range tc.wantRows {
				if i >= len(rows) {
					t.Errorf("row[%d] missing, want ContributionID=%q Amount=%v", i, want.ContributionID, want.Amount)
					continue
				}
				got := rows[i]
				if got.ContributionID != want.ContributionID {
					t.Errorf("row[%d] ContributionID: got %q, want %q", i, got.ContributionID, want.ContributionID)
				}
				if got.Amount != want.Amount {
					t.Errorf("row[%d] Amount: got %v, want %v", i, got.Amount, want.Amount)
				}
			}
		})
	}
}

// TestComputeDistribution_Individual covers cases 11–17 for Садовод-индивидуал payers.
func TestComputeDistribution_Individual(t *testing.T) {
	t.Skip("ComputeDistribution not yet implemented — remove skip once package exists")

	due := fixtures.ExampleDueIndividual // {INDIV_CURRENT:8000, TARGET_ROAD:5000, TARGET_FLOOD:3000, TARGET_DITCH:2050}

	cases := []distCase{
		{
			// Case 11: partial first bucket.
			name:         "case11_indiv_partial_1000",
			amount:       paymentFloat("PAY_1000"),
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 1,
			wantRows:     []DistributionRow{{ContributionID: "INDIV_CURRENT", Amount: 1000}},
		},
		{
			// Case 12: partial first bucket, larger.
			name:         "case12_indiv_partial_7000",
			amount:       paymentFloat("INDIV_PAY_7000"),
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 1,
			wantRows:     []DistributionRow{{ContributionID: "INDIV_CURRENT", Amount: 7000}},
		},
		{
			// Case 13: exact first bucket.
			name:         "case13_indiv_exact_8000",
			amount:       8000,
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 1,
			wantRows:     []DistributionRow{{ContributionID: "INDIV_CURRENT", Amount: 8000}},
		},
		{
			// Case 14: fills first bucket + partial second.
			name:         "case14_indiv_9000",
			amount:       paymentFloat("INDIV_PAY_9000"),
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 2,
			wantRows: []DistributionRow{
				{ContributionID: "INDIV_CURRENT", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 1000},
			},
		},
		{
			// Case 15: fills first two buckets exactly.
			name:         "case15_indiv_plus_road_13000",
			amount:       paymentFloat("INDIV_PLUS_ROAD_13000"),
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 2,
			wantRows: []DistributionRow{
				{ContributionID: "INDIV_CURRENT", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
			},
		},
		{
			// Case 16: exact full individual obligation.
			name:         "case16_indiv_full_18050",
			amount:       paymentFloat("FULL_TOTAL_18050"),
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 4,
			wantRows: []DistributionRow{
				{ContributionID: "INDIV_CURRENT", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
				{ContributionID: "TARGET_FLOOD", Amount: 3000},
				{ContributionID: "TARGET_DITCH", Amount: 2050},
			},
		},
		{
			// Case 17: overpayment advances to next year for individual.
			name:         "case17_indiv_overpay_18100",
			amount:       paymentFloat("OVERPAY_18100"),
			outstanding:  due,
			priority:     individualPriority,
			wantRowCount: 5,
			wantRows: []DistributionRow{
				{ContributionID: "INDIV_CURRENT", Amount: 8000},
				{ContributionID: "TARGET_ROAD", Amount: 5000},
				{ContributionID: "TARGET_FLOOD", Amount: 3000},
				{ContributionID: "TARGET_DITCH", Amount: 2050},
				{ContributionID: "INDIV_CURRENT", Amount: 50},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fields := OperationFields{Amount: tc.amount, Plot: individualPlot}
			rows, err := ComputeDistribution(fields, tc.outstanding, tc.priority)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rows) != tc.wantRowCount {
				t.Errorf("row count: got %d, want %d", len(rows), tc.wantRowCount)
			}
			for i, want := range tc.wantRows {
				if i >= len(rows) {
					t.Errorf("row[%d] missing: want %+v", i, want)
					continue
				}
				got := rows[i]
				if got.ContributionID != want.ContributionID {
					t.Errorf("row[%d] ContributionID: got %q, want %q", i, got.ContributionID, want.ContributionID)
				}
				if got.Amount != want.Amount {
					t.Errorf("row[%d] Amount: got %v, want %v", i, got.Amount, want.Amount)
				}
			}
		})
	}
}

// TestComputeDistribution_Sequential covers cases 18–20: sequential payments reducing debt.
// These simulate a payer who makes multiple payments throughout the year.
func TestComputeDistribution_Sequential(t *testing.T) {
	t.Skip("ComputeDistribution not yet implemented — remove skip once package exists")

	due := fixtures.ExampleDueMember

	t.Run("case18_sequential_member_5000_then_13050", func(t *testing.T) {
		// First payment: 5000 → partial MEMBER_REGULAR.
		fields1 := OperationFields{Amount: 5000, Plot: memberPlot}
		rows1, err := ComputeDistribution(fields1, due, memberPriority)
		if err != nil {
			t.Fatalf("payment1 error: %v", err)
		}
		if len(rows1) != 1 || rows1[0].ContributionID != "MEMBER_REGULAR" || rows1[0].Amount != 5000 {
			t.Errorf("payment1: unexpected rows: %+v", rows1)
		}
		// Remaining outstanding after first payment.
		remaining := map[string]float64{
			"MEMBER_REGULAR": due["MEMBER_REGULAR"] - 5000, // 3000
			"TARGET_ROAD":    due["TARGET_ROAD"],
			"TARGET_FLOOD":   due["TARGET_FLOOD"],
			"TARGET_DITCH":   due["TARGET_DITCH"],
		}
		// Second payment: 13050 = 3000 (finish MEMBER_REGULAR) + 5000 (TARGET_ROAD) + 3000 (TARGET_FLOOD) + 2050 (TARGET_DITCH).
		fields2 := OperationFields{Amount: 13050, Plot: memberPlot}
		rows2, err := ComputeDistribution(fields2, remaining, memberPriority)
		if err != nil {
			t.Fatalf("payment2 error: %v", err)
		}
		if len(rows2) != 4 {
			t.Errorf("payment2 row count: got %d, want 4. rows: %+v", len(rows2), rows2)
		}
	})

	t.Run("case19_sequential_member_2000_7000_9050", func(t *testing.T) {
		// Three incremental payments exhausting the full member obligation.
		// Payment 1: 2000 → partial MEMBER_REGULAR.
		remaining := copyMap(due)
		for _, payment := range []float64{2000, 7000, 9050} {
			fields := OperationFields{Amount: payment, Plot: memberPlot}
			rows, err := ComputeDistribution(fields, remaining, memberPriority)
			if err != nil {
				t.Fatalf("payment %.0f error: %v", payment, err)
			}
			// Deduct allocated amounts from remaining.
			for _, row := range rows {
				remaining[row.ContributionID] -= row.Amount
			}
		}
		// After all three payments the total paid must equal 18050.
		for k, v := range remaining {
			if v != 0 {
				t.Errorf("remaining[%s] = %.2f after full payment sequence, want 0", k, v)
			}
		}
	})

	t.Run("case20_large_overpay_spills_two_next_year_buckets", func(t *testing.T) {
		// 18050 (current year full) + 15500 advance → next year fills MEMBER_REGULAR (8000)
		// and partially TARGET_ROAD (7500) in fiscal_year+1.
		totalPayment := 18050 + 15500 // 33550
		fields := OperationFields{Amount: float64(totalPayment), Plot: memberPlot}
		nextDue := fixtures.NextYearDueMember
		allDue := mergeMaps(due, nextDue)
		rows, err := ComputeDistribution(fields, allDue, memberPriority)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Expect: 4 current-year rows + 2 next-year rows (MEMBER_REGULAR=8000, TARGET_ROAD=7500).
		if len(rows) != 6 {
			t.Errorf("row count: got %d, want 6. rows: %+v", len(rows), rows)
		}
	})
}

func copyMap(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mergeMaps(a, b map[string]float64) map[string]float64 {
	out := copyMap(a)
	for k, v := range b {
		out[k] += v
	}
	return out
}
