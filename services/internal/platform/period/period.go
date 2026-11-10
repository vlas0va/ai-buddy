// Package period maps an intern's cohort start date to the periods of the Avito checklist
// (data/checklist/checklist.yaml: week_1 … after_3_months) and gives each period its due offset and Russian label.
package period

import (
	"fmt"
	"slices"
	"time"
)

// Period is a checklist period id (openapi Period).
type Period string

// The checklist periods in order.
const (
	Week1        Period = "week_1"
	Week2        Period = "week_2"
	Month1       Period = "month_1"
	Month2       Period = "month_2"
	After3Months Period = "after_3_months"
)

type info struct {
	p       Period
	before  int // Current returns p while days since cohort start < before
	dueDays int
	label   string
}

// Same table as data/checklist/checklist.yaml (labels) and migrations/0003_seed_demo.sql (due offsets).
var table = []info{
	{Week1, 7, 7, "1 неделя"},
	{Week2, 14, 14, "2 неделя"},
	{Month1, 30, 30, "1 месяц"},
	{Month2, 90, 60, "2 месяц"},
	{After3Months, -1, 100, "После 3-х месяцев"},
}

// All returns the periods in checklist order (a fresh slice).
func All() []Period {
	out := make([]Period, len(table))
	for i, t := range table {
		out[i] = t.p
	}
	return out
}

// Parse returns s as a Period, or an error for an unknown id.
func Parse(s string) (Period, error) {
	p := Period(s)
	if Order(p) < 0 {
		return "", fmt.Errorf("period: unknown period %q", s)
	}
	return p, nil
}

// Order returns the 0-based position of p in checklist order, or -1 for an unknown period.
func Order(p Period) int {
	return slices.IndexFunc(table, func(t info) bool { return t.p == p })
}

// Label returns the Russian label («1 неделя» … «После 3-х месяцев»), or string(p) for an unknown period.
func Label(p Period) string {
	if i := Order(p); i >= 0 {
		return table[i].label
	}
	return string(p)
}

// DueOffsetDays returns the due date offset from the cohort start: 7, 14, 30, 60, 100; 0 for an unknown period.
func DueOffsetDays(p Period) int {
	if i := Order(p); i >= 0 {
		return table[i].dueDays
	}
	return 0
}

// DaysSince returns the number of calendar days from cohortStart to today (negative before the start).
// Each value is reduced to its calendar date in its own location; the time of day is ignored.
func DaysSince(cohortStart, today time.Time) int {
	return int(date(today).Sub(date(cohortStart)).Hours() / 24)
}

// Current returns the period of an intern on today: week_1 for days < 7 (also before the start), week_2 < 14,
// month_1 < 30, month_2 < 90, after_3_months otherwise. Days are counted by DaysSince.
func Current(cohortStart, today time.Time) Period {
	days := DaysSince(cohortStart, today)
	for _, t := range table {
		if t.before < 0 || days < t.before {
			return t.p
		}
	}
	return After3Months // unreachable: the last row has no bound
}

// DueDate returns cohortStart + DueOffsetDays(p) as a date (midnight UTC), like the seed's cohort_start + days.
func DueDate(cohortStart time.Time, p Period) time.Time {
	return date(cohortStart).AddDate(0, 0, DueOffsetDays(p))
}

// ParseDate parses a YYYY-MM-DD date (JWT claim cohort_start, DB dates) as midnight UTC.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("period: parse date %q: %w", s, err)
	}
	return t, nil
}

func date(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
