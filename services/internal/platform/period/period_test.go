package period

import (
	"slices"
	"testing"
	"time"
)

var start = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

func TestCurrent_Boundaries(t *testing.T) {
	tests := []struct {
		days int
		want Period
	}{
		{-3, Week1}, {0, Week1}, {6, Week1},
		{7, Week2}, {13, Week2},
		{14, Month1}, {29, Month1},
		{30, Month2}, {89, Month2},
		{90, After3Months}, {400, After3Months},
	}
	for _, tt := range tests {
		today := start.AddDate(0, 0, tt.days)
		if got := Current(start, today); got != tt.want {
			t.Errorf("day %d: got %s want %s", tt.days, got, tt.want)
		}
	}
}

func TestDaysSince_IgnoresTimeOfDayAndZone(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	// 2026-11-09 01:00 по Москве — это ещё 2026-11-08 по UTC; считается календарная дата в собственном поясе.
	today := time.Date(2026, 11, 9, 1, 0, 0, 0, msk)
	if got := DaysSince(start, today); got != 7 {
		t.Fatalf("got %d", got)
	}
	if got := DaysSince(start.Add(23*time.Hour), start.AddDate(0, 0, 1)); got != 1 {
		t.Fatalf("time of day must be ignored, got %d", got)
	}
	// Через переход на летнее время в поясе, где он есть.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err == nil {
		a := time.Date(2026, 3, 28, 12, 0, 0, 0, berlin)
		b := time.Date(2026, 3, 30, 0, 30, 0, 0, berlin)
		if got := DaysSince(a, b); got != 2 {
			t.Fatalf("DST: got %d", got)
		}
	}
}

func TestTable(t *testing.T) {
	tests := []struct {
		p     Period
		order int
		due   int
		label string
	}{
		{Week1, 0, 7, "1 неделя"},
		{Week2, 1, 14, "2 неделя"},
		{Month1, 2, 30, "1 месяц"},
		{Month2, 3, 60, "2 месяц"},
		{After3Months, 4, 100, "После 3-х месяцев"},
		{Period("week_9"), -1, 0, "week_9"},
	}
	for _, tt := range tests {
		if Order(tt.p) != tt.order || DueOffsetDays(tt.p) != tt.due || Label(tt.p) != tt.label {
			t.Errorf("%s: order %d due %d label %q", tt.p, Order(tt.p), DueOffsetDays(tt.p), Label(tt.p))
		}
	}
	all := All()
	if !slices.Equal(all, []Period{Week1, Week2, Month1, Month2, After3Months}) {
		t.Fatalf("All %v", all)
	}
	all[0] = "x"
	if All()[0] != Week1 {
		t.Fatal("All must return a copy")
	}
}

func TestParseAndDates(t *testing.T) {
	if p, err := Parse("month_2"); err != nil || p != Month2 {
		t.Fatalf("Parse: %v %v", p, err)
	}
	if _, err := Parse("month_3"); err == nil {
		t.Fatal("unknown period accepted")
	}
	d, err := ParseDate("2026-11-02")
	if err != nil || !d.Equal(start) {
		t.Fatalf("ParseDate: %v %v", d, err)
	}
	if _, err := ParseDate("02.11.2026"); err == nil {
		t.Fatal("bad date accepted")
	}
	if got := DueDate(start.Add(15*time.Hour), Month2); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("DueDate %v", got)
	}
}
