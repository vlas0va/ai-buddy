package escalations

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// A mentor's answer added to the knowledge base must be valid for every intern (checkpoint 11.11): the question and the
// answer lose greetings, names of people and concrete dates before kb.add_faq.

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
	greetingRe = regexp.MustCompile(`(?i)^\s*(привет|здравствуй(те)?|добрый\s+(день|вечер)|доброе\s+утро)[\s,!.]*`)
	spacesRe   = regexp.MustCompile(`[ \t]{2,}`)
	punctRe    = regexp.MustCompile(`\s+([,.!?:;])`)
	leadingRe  = regexp.MustCompile(`^[\s,.!:;—-]+`)
)

// generalize removes greetings, names (full name, first name or surname with Russian endings) and dates from a text.
func generalize(text string, names []string) string {
	out := strings.TrimSpace(text)
	for _, rx := range nameRes(names) {
		out = rx.ReplaceAllStringFunc(out, func(m string) string {
			// keep the character before the name that the pattern consumed
			if r := []rune(m); len(r) > 0 && !isLetter(r[0]) {
				return string(r[0])
			}
			return ""
		})
	}
	out = greetingRe.ReplaceAllString(leadingRe.ReplaceAllString(out, ""), "")
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

// nameRes builds one pattern per name word (≥ 3 letters, capitalised): the stem without its last vowel plus an ending.
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
	sort.Slice(words, func(i, j int) bool { return len(words[i]) > len(words[j]) }) // longer names first
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
