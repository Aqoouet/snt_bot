package bot

import (
	"strings"
	"testing"

	"snt-bot/internal/db"
	"snt-bot/internal/distribution"
)

func TestFormatPreviewSingleRow(t *testing.T) {
	rows := []distribution.DistributionRow{{
		ContributionID: "MEMBER_REGULAR",
		Direction:      "приход",
		Amount:         1000,
		FiscalYear:     2026,
		Membership:     "Член",
		Plot:           "5",
		PaymentType:    "Наличные",
	}}

	got := formatPreview(rows, 500)

	for _, want := range []string{
		"📋 Предпросмотр \\(1 строк\\)",
		"| Категория      | Напр.  | Сумма   | Год  | Членство | Участок | Платеж   | Баланс после |",
		"| MEMBER_REGULAR | приход | 1000.00 | 2026 | Член     | 5       | Наличные | 1500.00      |",
		"💳 Итоговый баланс: 1500\\.00 руб\\.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatPreviewMultiRowProjectedBalance(t *testing.T) {
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

	got := formatPreview(rows, 1000)

	for _, want := range []string{
		"TARGET_ROAD",
		"TARGET_DITCH",
		"1300.00",
		"1250.00",
		"💳 Итоговый баланс: 1250\\.00 руб\\.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatBalanceMessage(t *testing.T) {
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

	got := formatBalanceMessage(1249.5, 1000, 250.5, ops)

	for _, want := range []string{
		"📊 Баланс: 1249\\.50 руб\\.",
		"⬆️ Приход всего: 1000\\.00 руб\\.",
		"⬇️ Расход всего: 250\\.50 руб\\.",
		"🕐 Последние 2 операций",
		"| Дата       | Напр.  | Сумма   | Участок | Категория      | Членство | Платеж   | Баланс после |",
		"| 15.05.2026 | приход | 1000.00 | 5       | MEMBER_REGULAR | Член     | Наличные | 1500.00      |",
		"| 16.05.2026 | расход | 250.50  | 7       | Хозтовары      | -        | Счет     | 1249.50      |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("balance message missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatMarkdownTableSanitizesCells(t *testing.T) {
	got := formatMarkdownTable(
		[]string{"A", "B"},
		[][]string{{"foo|bar", "line1\nline2 `x` \\"}},
	)

	for _, unwanted := range []string{"foo|bar", "\nline2", "`x`", "\\"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("table contains unsanitized fragment %q in:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "foo/bar") {
		t.Fatalf("expected pipe to be sanitized in:\n%s", got)
	}
	if !strings.Contains(got, "line1 line2 'x' /") {
		t.Fatalf("expected multiline/backtick/backslash sanitization in:\n%s", got)
	}
}
