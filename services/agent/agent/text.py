"""Russian text normalisation, tokenisation and stemming shared by NLU and retrieval."""

from __future__ import annotations

import re
from functools import lru_cache

import snowballstemmer

_WORD_RE = re.compile(r"[a-zа-я0-9]+(?:[-.][a-zа-я0-9]+)*", re.IGNORECASE)
_STEMMER_RU = snowballstemmer.stemmer("russian")
_STEMMER_EN = snowballstemmer.stemmer("english")

STOPWORDS = frozenset(
    [
        "и",
        "в",
        "во",
        "не",
        "что",
        "он",
        "на",
        "я",
        "с",
        "со",
        "как",
        "а",
        "то",
        "все",
        "она",
        "так",
        "его",
        "но",
        "да",
        "ты",
        "к",
        "у",
        "же",
        "вы",
        "за",
        "бы",
        "по",
        "только",
        "ее",
        "мне",
        "было",
        "вот",
        "от",
        "меня",
        "еще",
        "нет",
        "о",
        "из",
        "ему",
        "теперь",
        "когда",
        "даже",
        "ну",
        "вдруг",
        "ли",
        "если",
        "уже",
        "или",
        "ни",
        "быть",
        "был",
        "него",
        "до",
        "вас",
        "нибудь",
        "опять",
        "уж",
        "вам",
        "ведь",
        "там",
        "потом",
        "себя",
        "ничего",
        "ей",
        "может",
        "они",
        "тут",
        "где",
        "есть",
        "надо",
        "ней",
        "для",
        "мы",
        "тебя",
        "их",
        "чем",
        "была",
        "сам",
        "чтоб",
        "без",
        "будто",
        "чего",
        "раз",
        "тоже",
        "себе",
        "под",
        "будет",
        "ж",
        "тогда",
        "этот",
        "того",
        "потому",
        "этого",
        "какой",
        "совсем",
        "ним",
        "здесь",
        "этом",
        "один",
        "почти",
        "мой",
        "тем",
        "чтобы",
        "нее",
        "сейчас",
        "были",
        "куда",
        "зачем",
        "всех",
        "никогда",
        "можно",
        "при",
        "наконец",
        "два",
        "об",
        "другой",
        "хоть",
        "после",
        "над",
        "больше",
        "тот",
        "через",
        "эти",
        "нас",
        "про",
        "всего",
        "них",
        "какая",
        "много",
        "разве",
        "три",
        "эту",
        "моя",
        "впрочем",
        "хорошо",
        "свою",
        "этой",
        "перед",
        "иногда",
        "лучше",
        "чуть",
        "том",
        "нельзя",
        "такой",
        "им",
        "более",
        "всегда",
        "конечно",
        "всю",
        "между",
        "это",
        "мне",
        "пожалуйста",
        "такое",
        "такие",
        "такая",
        "таких",
        "привет",
        "подскажи",
        "скажи",
        "расскажи",
        "можешь",
        "хочу",
        "нужно",
        "нужен",
        "нужна",
        "нужны",
        "мой",
        "моя",
        "мои",
        "моё",
        "мое",
    ]
)


def normalize(text: str) -> str:
    return (text or "").lower().replace("ё", "е").strip()


def words(text: str) -> list[str]:
    return _WORD_RE.findall(normalize(text))


@lru_cache(maxsize=20000)
def stem(word: str) -> str:
    if re.fullmatch(r"[a-z0-9.\-]+", word):
        return _STEMMER_EN.stemWord(word)
    return _STEMMER_RU.stemWord(word)


def stems(text: str, drop_stopwords: bool = True) -> list[str]:
    return [stem(w) for w in words(text) if not (drop_stopwords and w in STOPWORDS)]


def char_ngrams(text: str, n_min: int = 3, n_max: int = 5) -> list[str]:
    grams: list[str] = []
    for w in words(text):
        padded = f"<{w}>"
        for n in range(n_min, n_max + 1):
            grams.extend(padded[i : i + n] for i in range(max(len(padded) - n + 1, 0)))
    return grams


_NAME_ENDINGS = ("ой", "ей", "ом", "ем", "ою", "ую", "у", "ю", "а", "я", "ы", "и", "е", "о")


def name_stem(word: str) -> str:
    """«Анны», «Анне», «Анна» → «анн»: crude Russian first-name stem for matching people mentioned in a message."""
    w = normalize(word)
    for end in _NAME_ENDINGS:
        if w.endswith(end) and len(w) - len(end) >= 3:
            return w[: -len(end)]
    return w


def same_person(mention: str, full_name: str) -> bool:
    first = (full_name.split() or [""])[0]
    a, b = name_stem(mention), name_stem(first)
    return bool(a) and bool(b) and (a.startswith(b) or b.startswith(a))


# Colloquial words interns actually type → the words the test KB is written with. Replacing (not appending) keeps the
# coverage check honest: «будет ли у меня рабочий ноут?» must find «Ноутбук и техника», not escalate.
_SLANG = [
    (r"ноут\w*|комп|компа|компу|макбук\w*|макбоок|ноутбук\w*", "ноутбук"),
    (r"впн|vpn", "vpn сеть"),
    (r"вай-?фай|wi-?fi|wifi|интернет\w*", "сеть"),
    (r"репа|репу|репе|репы|репо|репка|repo", "репозиторий"),
    (r"пул+-?реквест\w*|пр|мр|pr|mr|мерж-?реквест\w*|merge-?request\w*", "pull request"),
    (r"смерж\w*|мерж\w*|влить", "merge"),
    (r"джир\w*|жир[аеуы]|jira", "jira"),
    (r"конф[аеуы]|конфлюенс\w*|confluence", "confluence"),
    (r"битбакет\w*|bitbucket|гитлаб\w*", "bitbucket"),
    (r"гит|git", "git"),
    (r"док[аиуе]|докум\w*", "документ"),
    (r"ревью\w*|ревьюер\w*|ревьювер\w*|review", "ревью"),
    (r"сикрет\w*|токен\w*|ключ[иейа]?\s+от\s+\w+", "секрет"),
    (r"дев(-|\s)?стенд\w*|dev-?стенд\w*|стенд(?!ап)\w*", "dev-стенд"),
    (r"деплой\w*|задепло\w*|выкат\w*|выкладк\w*|релиз\w*", "деплой ci/cd"),
    (r"мейл\w*|имейл\w*|e-?mail|почт\w*", "почта"),
    (r"зарплатн\w*\s+карт\w*", "банковская карта"),
    (r"тимлид\w*|лид[аеу]?", "руководитель"),
    (r"ментор\w*|бадди|buddy", "наставник"),
]
_SLANG_RE = [(re.compile(rf"(?<![\w-])(?:{p})(?![\w-])", re.IGNORECASE), repl) for p, repl in _SLANG]


def canonical_query(text: str) -> str:
    out = normalize(text)
    for rx, repl in _SLANG_RE:
        out = rx.sub(repl, out)
    return out


# --- FAQ for every intern (checkpoint 11.11): no greetings, names or dates (= B's core/escalations.generalize) ---------
ANY_DAY = "в нужный день"
_MONTHS = r"(?:январ|феврал|март|апрел|ма[йя]|июн|июл|август|сентябр|октябр|ноябр|декабр)\w*"
_WEEKDAYS = r"(?:понедельник|вторник|сред[ау]|четверг|пятниц[ау]|суббот[ау]|воскресенье)"
_PREP = r"(?:(?:на|в|во|с|со|до|к|по)\s+)?"
_DATE_RES = [
    re.compile(rf"\b{_PREP}\d{{1,2}}\s*(?:-|–|—|по|до)\s*\d{{1,2}}\s+{_MONTHS}(?:\s+\d{{4}}(?:\s+года?)?)?", re.I),
    re.compile(rf"\b{_PREP}\d{{1,2}}\s+{_MONTHS}(?:\s+\d{{4}}(?:\s+года?)?)?", re.I),
    re.compile(rf"\b{_PREP}\d{{1,2}}[./]\d{{1,2}}(?:[./]\d{{2,4}})?\b", re.I),
    re.compile(rf"\b{_PREP}\d{{4}}-\d{{2}}-\d{{2}}\b", re.I),
    re.compile(
        rf"\b(?:{_PREP}(?:сегодня|завтра|послезавтра|вчера)|(?:в|во|на|до|к)\s+{_WEEKDAYS}|на\s+(?:этой|следующей|прошлой)\s+неделе)\b",
        re.I,
    ),
]
_GREETING = re.compile(r"^\s*(привет|здравствуй(те)?|добрый\s+(день|вечер)|доброе\s+утро)[\s,!.]*", re.I)
_LEADING = re.compile(r"^[\s,.!:;—-]+")


def generalize(text: str, names: list[str]) -> str:
    """Remove greetings, people's names (with Russian endings) and concrete dates from a question or an answer."""
    out = text.strip()
    words = sorted({w for n in names for w in n.split() if len(w) >= 3}, key=len, reverse=True)
    for w in words:
        stem = w.rstrip("аяоеьйиыу")
        stem = stem if len(stem) >= 3 else w
        out = re.sub(rf"(?<!\w){re.escape(stem)}[а-яё]{{0,3}}(?:[,!]\s*)?", "", out)
    out = _GREETING.sub("", _LEADING.sub("", out))
    for rx in _DATE_RES:
        out = rx.sub(ANY_DAY, out)
    lines = []
    for line in out.split("\n"):
        line = re.sub(r"[ \t]{2,}", " ", line)
        line = re.sub(r"\s+([,.!?:;])", r"\1", line).replace(",,", ",")
        line = _LEADING.sub("", line.rstrip())
        lines.append(line[:1].upper() + line[1:])
    return "\n".join(lines).strip()
