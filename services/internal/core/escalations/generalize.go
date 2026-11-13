package escalations

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Ответ наставника, добавленный в базу знаний, должен подходить любому стажёру (чекпоинт 11.11): из вопроса и ответа
// перед kb.add_faq убираются приветствия, имена людей и конкретные даты.

const anyDay = "в нужный день"

var (
	months   = `(январ|феврал|март|апрел|ма[йя]|июн|июл|август|сентябр|октябр|ноябр|декабр)\p{L}*`
	weekdays = `(понедельник|вторник|сред[ау]|четверг|пятниц[ау]|суббот[ау]|воскресенье)`
	prep     = `(?:(?:на|в|во|с|со|до|к|по)\s+)?`
	dateRes  = []*regexp.Regexp{
		// «с 13 по 15 ноября», «13–15 ноября»
		regexp.MustCompile(`(?i)` + prep + `\d{1,2}\s*(?:-|–|—|по|до)\s*\d{1,2}\s+` + months + `(?:\s+\d{4}(?:\s+года?)?)?`),
		regexp.MustCompile(`(?i)` + prep + `\d{1,2}\s+` + months + `(?:\s+\d{4}(?:\s+года?)?)?`),
		regexp.MustCompile(`(?i)` + prep + `\d{1,2}[./]\d{1,2}(?:[./]\d{2,4})?`),
		regexp.MustCompile(`(?i)` + prep + `\d{4}-\d{2}-\d{2}`),
		regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:` + prep + `(?:сегодня|завтра|послезавтра|вчера)|(?:в|во|на|до|к)\s+` + weekdays +
			`|на\s+(?:этой|следующей|прошлой)\s+неделе)(?:[^\p{L}]|$)`),
	}
	// обёртка черновика buddy наставнику (отзыв 12.13): «Подскажи, пожалуйста: … В базе знаний команды ответа не
	// нашёл.»
	draftRe = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(подскажи(те)?|можешь\s+подсказать)[\s,]*(пожалуйста)?[\s,:—-]*`),
		regexp.MustCompile(`(?i)\s*в\s+базе\s+знаний(\s+команды)?\s+ответа\s+не\s+нашёл\.?\s*$`),
	}
	greetingRe = regexp.MustCompile(`(?i)^\s*(привет|здравствуй(те)?|добрый\s+(день|вечер)|доброе\s+утро)[\s,!.]*`)
	spacesRe   = regexp.MustCompile(`[ \t]{2,}`)
	punctRe    = regexp.MustCompile(`\s+([,.!?:;])`)
	leadingRe  = regexp.MustCompile(`^[\s,.!:;—-]+`)
)

// generalize убирает из текста приветствия, имена (полное имя, имя или фамилию с русскими окончаниями) и даты.
func generalize(text string, names []string) string {
	out := strings.TrimSpace(text)
	for _, rx := range nameRes(names) {
		out = rx.ReplaceAllStringFunc(out, func(m string) string {
			// сохранить символ перед именем, который поглотил шаблон
			if r := []rune(m); len(r) > 0 && !isLetter(r[0]) {
				return string(r[0])
			}
			return ""
		})
	}
	out = greetingRe.ReplaceAllString(leadingRe.ReplaceAllString(out, ""), "")
	for _, rx := range draftRe {
		out = rx.ReplaceAllString(out, "")
	}
	for _, rx := range dateRes {
		out = rx.ReplaceAllStringFunc(out, func(m string) string {
			r := []rune(m)
			pre, post := "", ""
			if len(r) > 0 && !isLetter(r[0]) && !isDigit(r[0]) {
				pre = string(r[0])
			}
			if len(r) > 1 && !isLetter(r[len(r)-1]) && !isDigit(r[len(r)-1]) {
				post = string(r[len(r)-1])
			}
			return pre + anyDay + post
		})
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		l = spacesRe.ReplaceAllString(l, " ")
		l = punctRe.ReplaceAllString(l, "$1")
		l = strings.ReplaceAll(l, ",,", ",")
		l = leadingRe.ReplaceAllString(strings.TrimRight(l, " "), "")
		lines[i] = upperFirst(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// nameRes строит по шаблону на каждое слово имени (≥ 3 букв, с заглавной): основа без последней гласной плюс окончание.
func nameRes(names []string) []*regexp.Regexp {
	seen := map[string]bool{}
	var words []string
	for _, n := range names {
		for _, w := range strings.Fields(n) {
			if len([]rune(w)) >= 3 && !seen[w] {
				seen[w] = true
				words = append(words, w)
			}
		}
	}
	sort.Slice(words, func(i, j int) bool { return len(words[i]) > len(words[j]) }) // сначала более длинные имена
	var out []*regexp.Regexp
	for _, w := range words {
		stem := strings.TrimRight(w, "аяоеьйиыу")
		if len([]rune(stem)) < 3 {
			stem = w
		}
		out = append(out, regexp.MustCompile(`(^|[^\p{L}])`+regexp.QuoteMeta(stem)+`\p{Ll}{0,3}(?:[,!]\s*)?`))
	}
	return out
}

func isLetter(r rune) bool { return unicode.IsLetter(r) }
func isDigit(r rune) bool  { return unicode.IsDigit(r) }

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
