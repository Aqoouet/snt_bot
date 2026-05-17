package bot

import (
	"testing"

	"snt-bot/internal/db"
	"snt-bot/internal/distribution"
)

func TestBuildPreviewImageSingleRow(t *testing.T) {
	rows := []distribution.DistributionRow{{
		ContributionID: "MEMBER_REGULAR",
		Direction:      "приход",
		Amount:         1000,
		FiscalYear:     2026,
		Membership:     "Член",
		Plot:           "5",
		PaymentType:    "Наличные",
	}}

	png, err := buildPreviewImage(rows, 500)
	if err != nil {
		t.Fatalf("buildPreviewImage error: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("buildPreviewImage returned empty bytes")
	}
	// PNG magic bytes
	if png[0] != 0x89 || png[1] != 'P' || png[2] != 'N' || png[3] != 'G' {
		t.Fatal("result is not a valid PNG")
	}
}

func TestBuildPreviewImageMultiRow(t *testing.T) {
	rows := []distribution.DistributionRow{
		{
			ContributionID: "TARGET_ROAD",
			Direction:      "приход",
			Amount:         300,
			FiscalYear:     2026,
			Membership:     "Член",
			Plot:           "5",
			PaymentType:    "Карта",
		},
		{
			ContributionID: "TARGET_DITCH",
			Direction:      "расход",
			Amount:         50,
			FiscalYear:     2026,
			Membership:     "-",
			Plot:           "-",
			PaymentType:    "Счет",
		},
	}

	png, err := buildPreviewImage(rows, 1000)
	if err != nil {
		t.Fatalf("buildPreviewImage multi-row error: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("buildPreviewImage returned empty bytes")
	}
}

func TestBuildBalanceImage(t *testing.T) {
	ops := []db.OperationRow{
		{
			OpDate:       "15.05.2026",
			Direction:    "приход",
			Amount:       1000,
			Plot:         "5",
			Category:     "MEMBER_REGULAR",
			Membership:   "Член",
			PaymentType:  "Наличные",
			BalanceAfter: 1500,
		},
		{
			OpDate:       "16.05.2026",
			Direction:    "расход",
			Amount:       250.5,
			Plot:         "7",
			Category:     "Хозтовары",
			Membership:   "-",
			PaymentType:  "Счет",
			BalanceAfter: 1249.5,
		},
	}

	png, err := buildBalanceImage(1249.5, 1000, 250.5, ops)
	if err != nil {
		t.Fatalf("buildBalanceImage error: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("buildBalanceImage returned empty bytes")
	}
	if png[0] != 0x89 || png[1] != 'P' || png[2] != 'N' || png[3] != 'G' {
		t.Fatal("result is not a valid PNG")
	}
}

func TestSanitizeTableCell(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"foo|bar", "foo/bar"},
		{"line1\nline2 `x` \\", "line1 line2 'x' /"},
		{"", "-"},
		{"  spaces  ", "spaces"},
	}
	for _, c := range cases {
		got := sanitizeTableCell(c.in)
		if got != c.want {
			t.Errorf("sanitizeTableCell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
