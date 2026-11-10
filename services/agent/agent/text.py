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
