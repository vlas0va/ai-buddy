"""Russian date expressions → date ranges, working-day calendar (stub NLU for S3)."""

from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import date, timedelta

from .text import normalize

MONTHS = {
    "январ": 1,
    "янв": 1,
    "феврал": 2,
    "фев": 2,
    "март": 3,
    "мар": 3,
    "апрел": 4,
    "апр": 4,
    "ма": 5,
    "июн": 6,
    "июл": 7,
    "август": 8,
    "авг": 8,
    "сентябр": 9,
    "сен": 9,
    "сент": 9,
    "октябр": 10,
    "окт": 10,
    "ноябр": 11,
    "ноя": 11,
    "декабр": 12,
    "дек": 12,
}
MONTH_GEN = [
    "",
    "января",
    "февраля",
    "марта",
    "апреля",
    "мая",
    "июня",
    "июля",
    "августа",
    "сентября",
    "октября",
    "ноября",
    "декабря",
]
WEEKDAY_SHORT = ["пн", "вт", "ср", "чт", "пт", "сб", "вс"]
WEEKDAYS = {
    "понедельник": 0,
    "вторник": 1,
    "сред": 2,
    "четверг": 3,
    "пятниц": 4,
    "суббот": 5,
    "воскресень": 6,
}
NUMBER_WORDS = {
    "один": 1,
    "одна": 1,
    "одного": 1,
    "два": 2,
    "две": 2,
    "двух": 2,
    "пару": 2,
    "пара": 2,
    "три": 3,
    "трех": 3,
    "четыре": 4,
    "пять": 5,
    "неделю": 5,
}

# Test production calendar of the prototype (public holidays and moved days off). Synthetic policy data.
HOLIDAYS = frozenset(
    date.fromisoformat(d)
    for d in [
        "2026-01-01",
        "2026-01-02",
        "2026-01-05",
        "2026-01-06",
        "2026-01-07",
        "2026-01-08",
        "2026-01-09",
        "2026-02-23",
        "2026-03-09",
        "2026-05-01",
        "2026-05-11",
        "2026-06-12",
        "2026-11-04",
        "2026-12-31",
        "2027-01-01",
        "2027-01-04",
        "2027-01-05",
        "2027-01-06",
        "2027-01-07",
        "2027-01-08",
        "2027-02-23",
        "2027-03-08",
        "2027-05-03",
        "2027-05-10",
        "2027-06-14",
        "2027-11-04",
    ]
)

_MONTH_RE = r"(январ\w*|янв|феврал\w*|фев|март\w*|мар|апрел\w*|апр|ма[яй]|июн\w*|июл\w*|август\w*|авг|сентябр\w*|сент?|октябр\w*|окт|ноябр\w*|ноя|декабр\w*|дек)\.?"
_WD_RE = r"(понедельник\w*|вторник\w*|сред[ауеы]|четверг\w*|пятниц[ауеы]|суббот[ауеы]|воскресень[ея])"


@dataclass(frozen=True)
class DateRange:
    start: date
    end: date

    @property
    def single(self) -> bool:
        return self.start == self.end


def is_working_day(d: date) -> bool:
    return d.weekday() < 5 and d not in HOLIDAYS


def working_days(start: date, end: date) -> int:
    if end < start:
        return 0
    return sum(1 for i in range((end - start).days + 1) if is_working_day(start + timedelta(days=i)))


def add_working_days(start: date, n: int) -> date:
    """Return the date of the n-th working day counting `start` as the first one (if it is working)."""
    d = start
    while not is_working_day(d):
        d += timedelta(days=1)
    counted = 1
    while counted < n:
        d += timedelta(days=1)
        if is_working_day(d):
            counted += 1
    return d


def human_date(d: date) -> str:
    return f"{d.day} {MONTH_GEN[d.month]} ({WEEKDAY_SHORT[d.weekday()]})"


def human_range(r: DateRange) -> str:
    if r.single:
        return human_date(r.start)
    return f"с {human_date(r.start)} по {human_date(r.end)}"


def _month_num(token: str) -> int | None:
    token = token.rstrip(".")
    for prefix in sorted(MONTHS, key=len, reverse=True):
        if token.startswith(prefix):
            return MONTHS[prefix]
    return None


def _infer_year(day: int, month: int, today: date, year: int | None = None) -> date | None:
    try:
        if year is not None:
            return date(year if year > 100 else 2000 + year, month, day)
        candidate = date(today.year, month, day)
    except ValueError:
        return None
    if candidate < today - timedelta(days=60):
        try:
            candidate = date(today.year + 1, month, day)
        except ValueError:
            return None
    return candidate


def _weekday_date(token: str, today: date, next_week: bool) -> date:
    wd = next(v for k, v in WEEKDAYS.items() if token.startswith(k))
    if next_week:
        monday_next = today + timedelta(days=7 - today.weekday())
        return monday_next + timedelta(days=wd)
    delta = (wd - today.weekday()) % 7
    return today + timedelta(days=delta or 7)


def _parse_point(text: str, today: date) -> tuple[date, int] | None:
    """Find the first single date expression; return (date, position)."""
    candidates: list[tuple[int, date]] = []
    for m in re.finditer(r"\b(\d{1,2})\.(\d{1,2})(?:\.(\d{2,4}))?\b", text):
        d = _infer_year(int(m.group(1)), int(m.group(2)), today, int(m.group(3)) if m.group(3) else None)
        if d:
            candidates.append((m.start(), d))
    for m in re.finditer(rf"\b(\d{{1,2}})\s+{_MONTH_RE}", text):
        month = _month_num(m.group(2))
        d = _infer_year(int(m.group(1)), month, today) if month else None
        if d:
            candidates.append((m.start(), d))
    for word, delta in (("послезавтра", 2), ("завтра", 1), ("сегодня", 0)):
        for m in re.finditer(rf"\b{word}\b", text):
            if word == "завтра" and text[max(0, m.start() - 5) : m.start()].endswith("после"):
                continue
            candidates.append((m.start(), today + timedelta(days=delta)))
    for m in re.finditer(rf"\b(?:(следующ\w*)\s+)?{_WD_RE}", text):
        candidates.append((m.start(), _weekday_date(m.group(2), today, bool(m.group(1)))))
    m = re.search(r"\bчерез\s+(\d+|\w+)\s+(?:дн|день)", text)
    if m:
        n = int(m.group(1)) if m.group(1).isdigit() else NUMBER_WORDS.get(m.group(1), 0)
        if n:
            candidates.append((m.start(), today + timedelta(days=n)))
    if re.search(r"\bчерез\s+неделю\b", text):
        candidates.append((text.index("через неделю"), today + timedelta(days=7)))
    if not candidates:
        return None
    pos, d = min(candidates, key=lambda c: c[0])
    return d, pos


def _duration_days(text: str) -> int | None:
    m = re.search(r"\bна\s+(\d+|\w+)\s+(?:рабочих\s+)?(?:дн|день|дня)", text)
    if m:
        n = int(m.group(1)) if m.group(1).isdigit() else NUMBER_WORDS.get(m.group(1))
        return n if n and n > 0 else None
    if re.search(r"\bна\s+(?:всю\s+)?неделю\b", text):
        return 5
    return None


def parse_range(text: str, today: date) -> DateRange | None:
    """Parse expressions like «в пятницу», «завтра», «14 ноября», «14.11», «с 14 по 16 ноября», «с пн по ср», «на 2 дня»."""
    t = normalize(text)
    # «14-16 ноября», «с 14 по 16 ноября»
    m = re.search(rf"\b(?:с\s+)?(\d{{1,2}})\s*(?:-|–|—|по|до)\s*(\d{{1,2}})\s+{_MONTH_RE}", t)
    if m:
        month = _month_num(m.group(3))
        if month:
            end = _infer_year(int(m.group(2)), month, today)
            start = _infer_year(int(m.group(1)), month, today)
            if start and end and end >= start:
                return DateRange(start, end)
    # «с <point> по <point>»
    m = re.search(r"\bс\s+(.+?)\s+(?:по|до)\s+(.+)", t)
    if m:
        first = _parse_point(m.group(1), today)
        second = _parse_point(m.group(2), today)
        if first and second:
            start, end = first[0], second[0]
            if re.fullmatch(r"\d{1,2}", m.group(1).strip()) is None and end < start:
                end = end + timedelta(days=7) if (end + timedelta(days=7)) >= start else end
            if end >= start:
                return DateRange(start, end)
    point = _parse_point(t, today)
    if point is None:
        return None
    start = point[0]
    days = _duration_days(t)
    if days and days > 1:
        return DateRange(start, add_working_days(start, days))
    return DateRange(start, start)
