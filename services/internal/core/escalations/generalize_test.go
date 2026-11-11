package escalations

import "testing"

// Checkpoint 11.11: the FAQ added from a mentor's answer has no names and no dates, so it is valid for every intern.
func TestGeneralize_Checkpoint_NoNamesNoDates(t *testing.T) {
	names := []string{"Анна Котова", "Олег Смирнов", "Мария Лебедева"}
	for _, tc := range []struct{ in, want string }{
		{"Олег, привет! Можно ли взять отгул 13 ноября, если в пятницу релиз?", "Можно ли взять отгул в нужный день, если в нужный день релиз?"},
		{"Привет, Анна! Да, напиши Марии Лебедевой заранее, она согласует.", "Да, напиши заранее, она согласует."},
		{"Анне можно с 1 по 14 декабря, а завтра нельзя.", "Можно в нужный день, а в нужный день нельзя."},
		{"Отчёт сдаём 20.11.2026 в 2026-11-20.", "Отчёт сдаём в нужный день в нужный день."},
		{"Как настроить VPN?", "Как настроить VPN?"},
	} {
		if got := generalize(tc.in, names); got != tc.want {
			t.Errorf("generalize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}
