"""Сценарии наставничества по ответам организаторов (10.11): стажёры, которые перекладывают работу или вставляют ответ LLM,
не понимая его. Buddy учит, а не делает: «учись, а не делегируй», подготовка к код-ревью, разбор логов с маскированием
секретов, проверка знаний (тест) и бенефиты без денежных операций.

Спецификация: openspec/specs/coaching/spec.md (CO-01 … CO-14).
"""

from __future__ import annotations

import random
import re
from collections.abc import Iterator
from typing import Any

from ..retrieval import TEST_BADGE, sanitize_snippet
from ..sse import Event, status
from ..text import STOPWORDS, normalize, stem, stems, words
from .common import Turn, first_name, person_name, say

SECRET_PATTERNS = [
    (
        re.compile(
            r"(?i)\b(\w*?(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key)\w*)([ \t]*[=:][ \t]*)"
            r"(?![\s\"']*\*\*\*)([\"']?)[^\s\"']+\3"
        ),
        r"\1\2\3***\3",
    ),
    (re.compile(r"(?i)\b(bearer)\s+[a-z0-9._\-]{12,}"), r"\1 ***"),
    (re.compile(r"\bAKIA[0-9A-Z]{16}\b"), "AKIA***"),
    (re.compile(r"\beyJ[a-zA-Z0-9_\-]{10,}\.[a-zA-Z0-9_\-]{10,}\.[a-zA-Z0-9_\-]{5,}"), "<jwt скрыт>"),
    (re.compile(r"(?i)(postgres(?:ql)?|mysql|redis|mongodb)://([^:\s/]+):([^@\s]+)@"), r"\1://\2:***@"),
]
ERROR_HINTS = [
    (
        re.compile(r"(?i)permission denied \(publickey\)|could not read from remote repository"),
        "eng-repo-bitbucket",
        "SSH-ключ не добавлен в Bitbucket или ещё не выдан доступ к репозиторию.",
    ),
    (
        re.compile(r"(?i)connection refused|econnrefused|could not connect"),
        "eng-dev-stand",
        "Сервис или его зависимость не запущены: проверь `docker compose ps` и порты из README.",
    ),
    (
        re.compile(r"(?i)npm err!|elifecycle|module not found|cannot find module"),
        "frontend-onboarding",
        "Зависимости не установлены или устарели: выполни `npm ci` и повтори.",
    ),
    (
        re.compile(r"(?i)traceback|modulenotfounderror|importerror"),
        "eng-dev-stand",
        "Python не находит модуль: проверь окружение и установку зависимостей.",
    ),
    (
        re.compile(r"(?i)test(s)? failed|assertionerror|--- fail|failed tests"),
        "eng-cicd-gitops",
        "Упали тесты: воспроизведи падающий тест локально, прежде чем звать наставника.",
    ),
    (
        re.compile(r"(?i)panic:|nil pointer|segmentation fault"),
        "eng-dev-stand",
        "Падение процесса: найди первую строку стека в своём коде — там обычно и причина.",
    ),
]
ERROR_LINE = re.compile(r"(?i)error|exception|fatal|panic|denied|refused|err!|fail")
CANCEL = re.compile(r"(?i)\s*(стоп|хватит|отмена|отмени|не хочу|потом|пропусти)\W*")


def mask_secrets(text: str) -> tuple[str, int]:
    count = 0
    for rx, repl in SECRET_PATTERNS:
        text, n = rx.subn(repl, text)
        count += n
    return text, count


def _source_event(hits: list[dict[str, Any]]) -> Event:
    return Event(
        "sources",
        {
            "items": [
                {
                    "doc_id": h["doc_id"],
                    "title": h["title"],
                    "source_type": "test",
                    "url": h.get("url", ""),
                    "badge": TEST_BADGE,
                }
                for h in hits
            ]
        },
    )


def _doc_hit(turn: Turn, doc_id: str) -> dict[str, Any] | None:
    doc = turn.kb.get(doc_id, turn.user.role, turn.user.team_id) if turn.kb is not None else None
    if doc is None:
        return None
    return {"doc_id": doc.doc_id, "title": doc.title, "url": doc.url, "body": doc.body}


def _team_doc(turn: Turn, suffix: str) -> dict[str, Any] | None:
    for prefix in (turn.user.team_id, "common"):
        hit = _doc_hit(turn, f"{prefix}-{suffix}")
        if hit:
            return hit
    return None


def _section(body: str, heading_word: str) -> str:
    """Текст раздела markdown, в заголовке которого есть heading_word."""
    parts = re.split(r"(?m)^##\s+", body)
    for part in parts[1:]:
        title, _, rest = part.partition("\n")
        if heading_word.lower() in title.lower():
            return rest.strip()
    return ""


def _mentor_draft(turn: Turn, topic: str, body: str) -> Event:
    mentor = turn.contact("mentor")
    hello = f"{first_name(mentor)}, привет!" if mentor else "Привет!"
    return Event(
        "escalation_draft",
        {
            "to_role": "mentor",
            "to_user_id": (mentor or {}).get("user_id"),
            "to_name": person_name(mentor),
            "topic": topic,
            "text": f"{hello} {body}",
        },
    )


# ----------------------------------------------------------------------------- «не понимаю задачу»
def handle_task_help(turn: Turn, text: str) -> Iterator[Event]:
    """Как сдвинуться с места в задаче, не делая её за стажёра; источник — руководство команды по первой задаче."""
    hit = _doc_hit(turn, f"{turn.user.team_id}-first-task")
    if hit:
        yield _source_event([hit])
    mentor = turn.contact("mentor")
    who = person_name(mentor, "наставнику")
    yield from say(
        "Давай разберёмся по шагам — так ты быстрее поймёшь задачу и сможешь объяснить решение на ревью:\n"
        "1. Перечитай описание задачи в Jira и найди критерии готовности: что должно получиться и как это проверить.\n"
        "2. Сформулируй задачу одним-двумя предложениями своими словами: что на входе, что на выходе.\n"
        "3. Найди похожие pull request в репозитории команды и посмотри, как решали такие задачи.\n"
        "4. Выпиши конкретные вопросы: какой шаг непонятен и что ты уже попробовал.\n"
        f"5. С этим списком напиши {who} — вопросы с контекстом решаются за пару минут.\n\n"
        "Пришли мне описание задачи своими словами — помогу разбить её на шаги. Код за тебя я писать не буду, но "
        "подскажу, где искать для каждого шага."
    )


# ----------------------------------------------------------------------------- «сделай за меня»
def handle_learning(turn: Turn, text: str) -> Iterator[Event]:
    hit = _doc_hit(turn, "common-ai-usage")
    if hit:
        yield _source_event([hit])
    mentor = turn.contact("mentor")
    who = f" ({person_name(mentor)})" if mentor else ""
    yield from say(
        "Сделать задачу за тебя я не могу — и это в твоих интересах: на ревью и на экваторе тебе нужно объяснять каждую "
        "строку. Давай сделаем так, чтобы ты справился сам:\n"
        "1. Опиши задачу своими словами: что на входе, что на выходе, как проверить результат.\n"
        "2. Разбей её на 3–5 шагов — пришли их мне, я подскажу, где искать для каждого шага.\n"
        "3. Застрянешь на конкретном шаге — пришли код или ошибку, объясню, что происходит.\n"
        "4. Нейросети можно использовать для подсказок, но сдавать — только то, что понимаешь и проверил тестами "
        f"(см. «Как пользоваться нейросетями в работе»).\n\nЕсли за 30 минут не сдвинулся — напиши наставнику{who}: "
        "что пробовал и где застрял. Могу подготовить такое сообщение."
    )
    turn.meta["coached"] = True


# ----------------------------------------------------------------------------- код-ревью
def handle_code_review(turn: Turn, text: str) -> Iterator[Event]:
    yield status("searching_kb", "Смотрю правила код-ревью команды")
    review = _team_doc(turn, "code-review")
    branches = _doc_hit(turn, "eng-branches-pr")
    hits = [h for h in (review, branches) if h]
    if hits:
        yield _source_event(hits)
    body = review["body"] if review else ""
    who = _section(body, "Кого позвать")
    rules_part = body.split("## Кого позвать")[0]
    rules = [line.strip("- ").strip() for line in rules_part.splitlines() if line.startswith("- ")][:3]
    checklist = [
        "CI зелёный, все тесты проходят",
        "описание pull request заполнено: что сделано, как проверить, ссылка на задачу",
        "нет отладочного кода, закомментированных кусков и секретов",
        "ты можешь объяснить каждую строку изменения",
    ]
    lines = [
        "Сам код я не ревьюю — это делают люди из команды, и это важная часть обучения. Помогу подготовиться.",
        "",
        "Проверь себя перед ревью:",
    ] + [f"- {c}" for c in checklist]
    if rules:
        lines += ["", "Правила твоей команды:"] + [f"- {r}" for r in rules]
    if who:
        lines += ["", "Кого позвать:", sanitize_snippet(who)]
    yield from say("\n".join(lines))
    yield _mentor_draft(
        turn,
        "code_review",
        "Мой pull request готов к ревью: <ссылка>. CI зелёный, описание заполнено. Посмотришь, пожалуйста?",
    )


# ----------------------------------------------------------------------------- логи
CI_STEPS = (
    "Что делать, если CI красный:\n"
    "1. Открой упавший шаг пайплайна в pull request и найди первую ошибку — дальше обычно последствия.\n"
    "2. Посмотри, зелёная ли ветка `main`: если нет — упало не из-за тебя, напиши в чат команды.\n"
    "3. Запусти упавший тест локально (`make test` или команда из README сервиса) и воспроизведи ошибку.\n"
    "4. Тест «моргает» (то падает, то нет) — перезапусти шаг один раз; повторяется — это баг, не перезапускай бесконечно.\n"
    "5. Порог покрытия — 80% для нового кода: если упало на нём, допиши тесты.\n\n"
    "Пришли сюда текст ошибки из лога — разберу главную строку. Если не получится за 30 минут, напиши наставнику: "
    "ссылка на пайплайн, текст ошибки и что уже попробовал."
)


def handle_troubleshoot(turn: Turn, text: str) -> Iterator[Event]:
    masked, secrets = mask_secrets(text)
    if not ERROR_LINE.search(masked) and len(masked.strip().splitlines()) <= 2:
        # вопрос о сбое («упали тесты в CI»), а не лог: шаги, а источник — руководство по CI/CD
        hit = _doc_hit(turn, "eng-cicd-gitops")
        if hit:
            yield _source_event([hit])
        yield from say(CI_STEPS)
        return
    yield status("searching_kb", "Разбираю ошибку")
    hint = next(((doc, why) for rx, doc, why in ERROR_HINTS if rx.search(masked)), None)
    hit = _doc_hit(turn, hint[0]) if hint else None
    if hit:
        yield _source_event([hit])
    lines_in = [ln.strip() for ln in masked.strip().splitlines() if ln.strip()]
    first_error = next((ln for ln in lines_in if ERROR_LINE.search(ln)), lines_in[0] if lines_in else "")
    lines = []
    if secrets:
        lines.append(
            f"Внимание: в тексте были секреты ({secrets}) — я их скрыл. Не пересылай такие логи в чаты как есть, "
            "а если секрет попал в git — сразу скажи наставнику, ключ нужно перевыпустить."
        )
    lines.append(f"Главная строка ошибки: «{first_error[:200]}».")
    lines.append(
        f"Вероятная причина: {hint[1]}"
        if hint
        else "Точную причину по логу не определить. Найди первую строку стека в своём коде и воспроизведи ошибку локально."
    )
    lines.append(
        "Если не получится, приложи наставнику: команду, которую запускал, полный текст ошибки (без секретов) и что "
        "уже попробовал — черновик ниже."
    )
    yield from say("\n\n".join(lines))
    excerpt = "\n".join(lines_in[:8])[:600]
    yield _mentor_draft(
        turn,
        "troubleshoot",
        f"Не могу разобраться с ошибкой. Запускал: <команда>. Уже попробовал: <что>. Лог:\n{excerpt}",
    )
    turn.meta["secrets_masked"] = secrets


# ----------------------------------------------------------------------------- бенефиты
def handle_benefit(turn: Turn, text: str) -> Iterator[Event]:
    hit = _doc_hit(turn, "common-benefits-overview")
    intro = "Оформить бенефит сам я не могу — денежных операций у меня нет."
    # контракт: операция, которую buddy делать нельзя, всё равно остаётся отказом (reason out_of_scope, detail benefit,
    # R72) …
    yield Event(
        "refusal",
        {"reason": "out_of_scope", "detail": "benefit", "redirect_role": None, "redirect_name": None, "text": intro},
    )
    if hit:
        yield _source_event([hit])
    table = _table(hit["body"]) if hit else ""
    # … но стажёр всё равно получает общую справку с тестовыми цифрами и тем, как сделать самому (ответы 10.11).
    yield from say(
        intro
        + " Но подскажу, как сделать это самому (цифры — тестовые данные прототипа):\n\n"
        + table
        + "\n\nВопросы о личных выплатах — к HR BP."
    )


def _table(body: str) -> str:
    return "\n".join(line for line in body.splitlines() if line.startswith("|") and "---" not in line)


# ----------------------------------------------------------------------------- тест
QUIZ_TOPIC_NOISE = re.compile(
    r"(?i)провер\w*\s+(меня|мои\s+знания|себя|понимание)|квиз\w*|тест\w*\s+по|задай\s+(мне\s+)?(\d+\s+)?вопрос\w*"
    r"|закреп\w*|\bпо\b"
)


# задачи плана, подтверждаемые тестом (= «quiz» в core plan.confirmModes): полный балл отмечает их выполненными
# (организаторы 12.13)
QUIZ_ITEMS = {"CL-07", "CL-09"}
TASK_TITLE = re.compile(r"«([^»]{8,})»")


def _task_doc(turn: Turn, text: str) -> dict[str, Any] | None:
    """«Проверь меня по задаче «…»» из плана: документы пункта чек-листа этой задачи."""
    m = TASK_TITLE.search(text)
    if not m or turn.kb is None:
        return None
    title = normalize(m.group(1))
    task = next((t for t in turn.user.tasks if t.checklist_item_id and normalize(t.title).startswith(title[:40])), None)
    if task is None:
        return None
    docs = [
        d
        for d in turn.kb.docs
        if task.checklist_item_id in d.checklist
        and d.readable_by(turn.user.role, turn.user.team_id)
        and len(_candidates(d.body)) >= 2
    ]
    docs.sort(key=lambda d: (turn.user.team_id not in d.teams, -len(_candidates(d.body))))  # сначала своя команда
    if not docs:
        return None
    d = docs[0]
    return {
        "doc_id": d.doc_id,
        "title": d.title,
        "url": d.url,
        "body": d.body,
        "task": task.title,
        "item": task.checklist_item_id,
    }


def _quiz_doc(turn: Turn, text: str) -> dict[str, Any] | None:
    if turn.kb is None:
        return None
    by_task = _task_doc(turn, text)
    if by_task:
        return by_task
    query = QUIZ_TOPIC_NOISE.sub(" ", text).strip()
    if stems(query):
        hits = turn.kb.search(query, turn.user.role, turn.user.team_id, k=1)
        if hits and turn.kb.coverage(query, hits[0].doc.doc_id) >= 0.3 and len(_candidates(hits[0].doc.body)) >= 2:
            d = hits[0].doc
            task = _task_obj(turn, d)
            return {
                "doc_id": d.doc_id,
                "title": d.title,
                "url": d.url,
                "body": d.body,
                "task": task.title if task else "",
                "item": task.checklist_item_id if task else "",
            }
    # без темы: первая открытая задача плана, для которой есть подходящий документ
    for t in turn.user.tasks:
        if t.status == "done" or not t.checklist_item_id:
            continue
        for d in turn.kb.docs:
            if (
                t.checklist_item_id in d.checklist
                and d.readable_by(turn.user.role, turn.user.team_id)
                and len(_candidates(d.body)) >= 3
            ):
                return {
                    "doc_id": d.doc_id,
                    "title": d.title,
                    "url": d.url,
                    "body": d.body,
                    "task": t.title,
                    "item": t.checklist_item_id,
                }
    return None


def _task_obj(turn: Turn, doc: Any) -> Any:
    for t in turn.user.tasks:
        if t.status != "done" and t.checklist_item_id and t.checklist_item_id in doc.checklist:
            return t
    return None


def _candidates(body: str) -> list[str]:
    out = []
    for line in body.splitlines():
        line = re.sub(r"^\s*(?:[-*]|\d+\.)\s+", "", line).strip()
        if 40 <= len(line) <= 220 and not line.startswith(("|", "#", ">")):
            out.append(re.sub(r"[*`]", "", line))
    return out


def _mask(line: str) -> tuple[str, str] | None:
    """Скрыть самое информативное слово предложения: число, иначе самое длинное слово не из стоп-слов."""
    tokens = re.findall(r"[A-Za-zА-Яа-яЁё0-9]+(?:[-.][A-Za-zА-Яа-яЁё0-9]+)*", line)
    nums = [t for t in tokens if t.isdigit()]
    pool = nums or sorted((t for t in tokens if len(t) >= 6 and normalize(t) not in STOPWORDS), key=len, reverse=True)
    if not pool:
        return None
    answer = pool[0]
    return line.replace(answer, "_____", 1), answer


def build_quiz(body: str, n: int = 3, seed: int = 0) -> list[dict[str, str]]:
    cands = _candidates(body)
    rnd = random.Random(seed)
    rnd.shuffle(cands)
    quiz = []
    for c in cands:
        m = _mask(c)
        if m:
            quiz.append({"q": m[0], "a": m[1]})
        if len(quiz) == n:
            break
    return quiz


def handle_quiz(turn: Turn, text: str) -> Iterator[Event]:
    doc = _quiz_doc(turn, text)
    quiz = build_quiz(doc["body"], seed=len(text)) if doc else []
    if not doc or len(quiz) < 2:
        yield from say(
            "Не нашёл подходящего документа для проверки. Напиши тему, например: «проверь меня по защите данных» "
            "или «задай вопросы по код-ревью»."
        )
        return
    yield _source_event([doc])
    state = turn.state()
    state["quiz"] = {
        "doc_id": doc["doc_id"],
        "title": doc["title"],
        "answers": [q["a"] for q in quiz],
        "task": doc.get("task", ""),
        "item": doc.get("item", ""),
    }
    turn.save_state(state)
    lines = [
        f"Проверим понимание по тестовой инструкции «{doc['title']}». Вставь пропущенные слова — ответь одним "
        "сообщением, через запятую или с новой строки (или напиши «стоп»):"
    ]
    lines += [f"{i}. {q['q']}" for i, q in enumerate(quiz, 1)]
    yield from say("\n".join(lines))


def _same(a: str, b: str) -> bool:
    a, b = normalize(a).strip(" .,"), normalize(b).strip(" .,")
    if not a:
        return False
    if a == b:
        return True
    wa, wb = words(a), words(b)
    return bool(wa) and bool(wb) and stem(wa[0]) == stem(wb[0])


def handle_quiz_answer(turn: Turn, text: str) -> Iterator[Event]:
    state = turn.state()
    quiz = state.pop("quiz", None)
    turn.save_state(state)
    if not quiz:
        yield from say("Активной проверки нет. Напиши «проверь меня по …», чтобы начать.")
        return
    if CANCEL.fullmatch(text):
        yield from say("Хорошо, проверку остановил. Вернёмся, когда будешь готов.")
        return
    parts = [re.sub(r"^\s*\d+[.)]\s*", "", p).strip() for p in re.split(r"[,;\n]+", text) if p.strip()]
    answers = quiz["answers"]
    results = []
    for i, exp in enumerate(answers):
        got = parts[i] if i < len(parts) else ""
        results.append((i, exp, got, _same(got, exp)))
    score = sum(ok for *_, ok in results)
    lines = [f"Результат: {score} из {len(answers)}."]
    for i, exp, got, ok in results:
        mark = "верно" if ok else f"нужно: {exp}" + (f" (у тебя: {got})" if got else "")
        lines.append(f"{i + 1}. {mark}")
    passed = score == len(answers)
    if passed:
        tail = "Отличный результат!"
        if quiz.get("task") and quiz.get("item") in QUIZ_ITEMS and turn.user.role == "intern":
            tail += f" Задача плана «{quiz['task']}» отмечена выполненной."
        elif quiz.get("task"):
            tail += f" Можно отметить задачу плана «{quiz['task']}» как выполненную."
        lines.append(tail)
    else:
        lines.append(f"Перечитай «{quiz['title']}» и попробуй ещё раз — напиши «проверь меня по …» снова.")
    turn.meta["quiz_score"] = f"{score}/{len(answers)}"
    yield from say("\n".join(lines))
    if passed and quiz.get("item") in QUIZ_ITEMS and turn.user.role == "intern":
        yield Event("quiz_passed", {"checklist_item_id": quiz["item"]})  # ядро отмечает задачу вызывающего выполненной
