// Пакет period сопоставляет дату начала когорты стажёра этапам чек-листа Авито (data/checklist/checklist.yaml: week_1 …
// after_3_months) и даёт каждому этапу смещение срока и русскую подпись.
package period

import (
	"fmt"
	"slices"
	"time"
)

// Period — id этапа чек-листа (openapi Period).
type Period string

// Этапы чек-листа по порядку.
const (
	Week1        Period = "week_1"
	Week2        Period = "week_2"
	Month1       Period = "month_1"
	Month2       Period = "month_2"
	After3Months Period = "after_3_months"
)

type info struct {
	p       Period
	before  int // Current возвращает p, пока дней с начала когорты < before
	dueDays int
	label   string
}

// Та же таблица, что в data/checklist/checklist.yaml (подписи) и migrations/0003_seed_demo.sql (смещения сроков).
var table = []info{
	{Week1, 7, 7, "1 неделя"},
	{Week2, 14, 14, "2 неделя"},
	{Month1, 30, 30, "1 месяц"},
	{Month2, 90, 60, "2 месяц"},
	{After3Months, -1, 100, "После 3-х месяцев"},
}

// All возвращает этапы в порядке чек-листа (новый срез).
func All() []Period {
	out := make([]Period, len(table))
	for i, t := range table {
		out[i] = t.p
	}
	return out
}

// Parse возвращает s как Period или ошибку для неизвестного id.
func Parse(s string) (Period, error) {
	p := Period(s)
	if Order(p) < 0 {
		return "", fmt.Errorf("period: unknown period %q", s)
	}
	return p, nil
}

// Order возвращает позицию p в порядке чек-листа (с 0) или -1 для неизвестного этапа.
func Order(p Period) int {
	return slices.IndexFunc(table, func(t info) bool { return t.p == p })
}

// Label возвращает русскую подпись («1 неделя» … «После 3-х месяцев») или string(p) для неизвестного этапа.
func Label(p Period) string {
	if i := Order(p); i >= 0 {
		return table[i].label
	}
	return string(p)
}

// DueOffsetDays возвращает смещение срока от начала когорты: 7, 14, 30, 60, 100; 0 для неизвестного этапа.
func DueOffsetDays(p Period) int {
	if i := Order(p); i >= 0 {
		return table[i].dueDays
	}
	return 0
}

// DaysSince возвращает число календарных дней от cohortStart до today (отрицательное до начала). Каждое значение
// приводится к календарной дате в своём часовом поясе; время суток игнорируется.
func DaysSince(cohortStart, today time.Time) int {
	return int(date(today).Sub(date(cohortStart)).Hours() / 24)
}

// Current возвращает этап стажёра на today: week_1 при днях < 7 (и до начала), week_2 < 14, month_1 < 30, month_2 < 90,
// иначе after_3_months. Дни считаются через DaysSince.
func Current(cohortStart, today time.Time) Period {
	days := DaysSince(cohortStart, today)
	for _, t := range table {
		if t.before < 0 || days < t.before {
			return t.p
		}
	}
	return After3Months // недостижимо: у последней строки нет границы
}

// DueDate возвращает cohortStart + DueOffsetDays(p) как дату (полночь UTC), как cohort_start + days в демо-данных.
func DueDate(cohortStart time.Time, p Period) time.Time {
	return date(cohortStart).AddDate(0, 0, DueOffsetDays(p))
}

// ParseDate разбирает дату YYYY-MM-DD (claim JWT cohort_start, даты из БД) как полночь UTC.
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
