"""Intent routing: deterministic guardrails (layer 0 of ADR-003) → high-precision rules → NB classifier → threshold."""

from __future__ import annotations

import re
from dataclasses import dataclass, field

from ..text import normalize
from .classifier import IntentModel

# ----------------------------------------------------------------------------- guardrail patterns
INJECTION = re.compile(
    r"(игнорир\w*|забудь|отключи|обойди|ignore|disregard|forget)\s+(\w+\s+){0,3}(инструкц|правил|ограничен|instruction|rule|prompt)"
    r"|системн\w+\s+промпт|system\s*prompt|developer\s*mode|режим\w*\s+(разработчика|бога|админ)|jailbreak|dan\s+mode"
    r"|ты\s+теперь\s+(не\s+)?(buddy|бадди|ассистент|админ)|притворись|представь,?\s+что\s+ты"
    r"|\b(approvals|absence|access|directory|kb|checklist|trips|consents)\.[a-z_]+",
    re.IGNORECASE,
)
SENSITIVE = re.compile(
    # Personal money only: general benefits with test figures are answered from the KB (organizers' Q&A, 10.11).
    r"зарплат|оклад|преми[яиюей]|бонус\w*|выплат|ндфл|налог|сколько\s+(\w+\s+){0,3}(плат|заплат|получа|зарабат)|\bоплат\w*\s+(труда|стажировк)"
    r"|сколько\s+денег\s+(я|мне|у\s+меня)|мо\w+\s+(доход|начислен)|оплат\w*\s+(отпуск|больнич)"
    r"|диагноз|медицинск\w*\s+(данн|сведен|карт|документ|справк)|медкарт|анализ\w*\s+(кров|результат)|результат\w*\s+анализ"
    r"|паспорт|снилс|\bинн\b|номер\w*\s+карт|банковск\w*\s+(карт|счет|реквизит)|реквизит",
    re.IGNORECASE,
)
HIRING_FIRING = re.compile(
    r"уволи|увольн|уволь|наня|найм|нанять|оффер\w*\s+кандидат|открыть\s+вакансию|собеседован\w*\s+кандидат",
    re.IGNORECASE,
)
# Booking and paying are the travel service's job; the trip request itself is the «trip» scenario.
TRIP_OPS = re.compile(
    r"(забронируй|бронируй|закажи|оплати|купи)\w*\s+(\w+\s+){0,2}(билет|отел|гостиниц|жиль|номер\s+в)", re.IGNORECASE
)
BENEFIT_OPS = re.compile(
    r"(оформи|подключи|начисли|верни|оплати|компенсируй|возмести)\w*\s+(\w+\s+){0,2}"
    r"(компенсац|бенефит|дмс|страховк|вычет|курс|обучени|конференц|спорт|фитнес|абонемент|такси|питани)",
    re.IGNORECASE,
)
HUMAN_ONLY = re.compile(
    r"(дай|дать|оставь|напиши)\s+(мне\s+)?(обратн\w+\s+связ|фидбек|feedback)|оцени\s+(меня|мою\s+работу|мой\s+прогресс|мои\s+компетенц)"
    r"|ревью\s+компетенц|проведи\s+(со\s+мной\s+)?(1:1|1-1|встречу|созвон)|(выступи|выступить|сходи|пройди)\s+(\w+\s+){0,3}(за\s+меня|вместо\s+меня)"
    r"|(напиши|сделай)\s+(за\s+меня\s+)?(мою\s+)?презентаци\w*\s+(за\s+меня|вместо\s+меня)|прими\s+решение\s+за\s+наставника",
    re.IGNORECASE,
)
OTHER_ACTION = re.compile(
    r"(оформи|создай|запроси|подай|возьми|отмени|согласуй|выдай|сделай)\w*\s+(\w+\s+){0,3}(за|для|вместо|от\s+имени)\s+"
    r"([А-ЯЁ][а-яё]+|коллег\w*|друг\w*\s+сотрудник\w*|него|нее|неё|них|сотрудник\w*)",
)
OTHER_DATA = re.compile(
    r"(отпуск|отгул|заявк|доступ|больничн|отсутстви|план|прогресс|задач|телефон|адрес|контакт|почт)\w*\s+(\w+\s+){0,2}"
    r"(у\s+)?(коллег\w*|чуж\w*|друг\w*\s+(?:сотрудник|стаж[её]р)\w*|всех\s+(?:сотрудник|стаж[её]р)\w*|всей\s+команды|[А-ЯЁ][а-яё]+[аыяиу]\b)"
    r"|(коллег\w*|чуж\w*|други\w+\s+стаж\w*|все\w*\s+стаж\w*)\s+(\w+\s+){0,2}(отпуск|отгул|заявк|доступ|больничн|план|прогресс|контакт)",
)
NOT_NAMES = {
    "Jira",
    "Confluence",
    "Grafana",
    "Авито",
    "Доставка",
    "Доставки",
    "Поиск",
    "Поиска",
    "HR",
    "BP",
    "Олег",
    "Мария",
    "Москва",
    "Москве",
    "Telegram",
    "Slack",
    "Git",
    "Bitbucket",
    "DLV",
    "SRCH",
    "Kafka",
    "Postgres",
    "Ok",
    "Да",
    "Нет",
}

# ----------------------------------------------------------------------------- high-precision intent rules
DELEGATE_WORK = re.compile(
    r"(сделай|напиши|реши|допиши|доделай|сгенерируй|выполни)\s+(\w+\s+){0,2}(за\s+меня|вместо\s+меня|мою\s+(задачу|домашк|работу)|мой\s+(тикет|таск))"
    r"|(напиши|сгенерируй)\s+(мне\s+)?(весь\s+|готовый\s+)?(код|решение)\s+(для|к|по)\s+(моей\s+)?(задач|тикет|таск|dlv-|srch-)"
    r"|сделай\s+(мою\s+)?(задачу|тикет)\s+(dlv|srch)-?\d*",
)
LOG_LINE = re.compile(
    r"traceback \(most recent call last\)|exception|error[:\s]|\bfatal:|panic:|npm err!|exit code \d+|stack ?trace"
    r"|\bat [\w.$]+\([\w.]+:\d+\)|segmentation fault|permission denied|connection refused|no such file",
    re.IGNORECASE,
)


def looks_like_log(original: str) -> bool:
    """Pasted logs or error output: several lines with error markers, or one long line with a stack frame."""
    hits = len(LOG_LINE.findall(original))
    return hits >= 2 or (hits >= 1 and ("\n" in original.strip() or len(original) > 160))


RULES: list[tuple[str, re.Pattern[str]]] = [
    (
        "one_on_one",
        re.compile(r"подготов\w*\s+(меня\s+)?к\s+(встрече\s+)?1\s*[:\-]\s*1|повестк\w*\s+(для\s+|к\s+)?1\s*[:\-]\s*1"),
    ),
    ("task_message", re.compile(r"(написать|напиши|сообщение)\s+(\w+\s+)?наставник\w*\s+(про|по|о)\s+задач\w*\s+«")),
    (
        "quiz",
        re.compile(
            r"провер\w*\s+(меня|мои\s+знания|себя|понимание)|квиз|тест\s+по\s+|задай\s+(мне\s+)?(\d+\s+)?вопрос|закреп\w*\s+(материал|знани)|проверочн"
        ),
    ),
    (
        "code_review",
        re.compile(
            r"поревью|отревью|ревьюн|(посмотри|проверь)\s+(мой\s+|мою\s+)?(pr|пр\b|пулл?\s*-?\s*реквест|merge\s*request|код|ветку)"
            r"|кого\s+(позвать|звать|назначить|позвать)\s+на\s+(код-?\s?)?ревью|(отдать|отправить)\s+(\w+\s+)?на\s+(код-?\s?)?ревью"
            r"|ревьюер|(pr|пулл?\s*-?\s*реквест)\s+(готов|на\s+ревью)"
        ),
    ),
    ("kb_question", re.compile(r"к\s+кому\s+(мне\s+)?(идти|обращаться|обратиться|пойти)")),
    (
        "trip_status",
        re.compile(
            r"(статус|что\s+с|как\s+там|где)\s+(\w+\s+){0,3}командировк|мои\s+командировк|список\s+(моих\s+)?командировок"
        ),
    ),
    (
        "absence_status",
        re.compile(
            r"(статус|что\s+с|как\s+там|где)\s+(\w+\s+){0,2}(мо\w+\s+)?(заявк|отгул|отпуск)|мои\s+заявк|список\s+(моих\s+)?заявок"
        ),
    ),
    (
        "approvals",
        re.compile(
            r"(согласу|одобр|отклон|утверд)\w*\s+(\w+\s+){0,2}(заявк|отгул|abs-|acc-)|на\s+согласовани|ожида\w+\s+(моего\s+)?согласован|заявки\s+(моей\s+)?команды|abs-\d+|acc-\d+|trp-\d+"
            r"|(согласу|одобр|отклон|утверд)\w*\s+(\w+\s+){0,2}командировк"
        ),
    ),
    (
        "trip",
        re.compile(
            r"командировк|(съездить|поехать|поездк\w*)\s+(\w+\s+)?в\s+(офис|казан|новосибирск|екатеринбург|питер|петербург|москв)"
        ),
    ),
    (
        "consents",
        re.compile(r"\bсоглас(ие|ия|ий|ию|иям|иях)\b|\bnda\b|соглаш\w+\s+о\s+(конфиденциальн|неразглашен)"),
    ),
    (
        "absence",
        re.compile(
            r"\b(отгул\w*|отпуск\w*|выходн\w+\s+день|за\s+свой\s+сч[её]т|без\s+сохранения|не\s+выйду|отсутств\w+)\b"
        ),
    ),
    ("channels", re.compile(r"\b(канал\w*|чат\w*)\b")),
    (
        "access",
        re.compile(
            r"\b(доступ\w*|права\s+на|confluence|конфлюенс|jira|джир\w*|репозитори\w*|grafana|графан\w*|пространств\w*)\b"
        ),
    ),
    (
        "summary",
        re.compile(
            r"(сводк|итог|резюм)\w*\s+(\w+\s+){0,3}(экватор|прогресс|задач|сделан)|что\s+я\s+(уже\s+)?сделал|для\s+презентации\s+на\s+экватор"
        ),
    ),
    (
        "plan",
        re.compile(
            r"(мой|мои|мою)\s+(план|задач)|следующ\w+\s+шаг|что\s+(мне\s+)?(делать|сделать)\s+(дальше|сейчас|сегодня|на\s+этой\s+неделе)|что\s+дальше|что\s+просрочен|просрочк|какой\s+(у\s+меня\s+)?этап|план\s+адаптации|чем\s+(мне\s+)?заняться|следующ\w+\s+задач|что\s+по\s+плану"
        ),
    ),
    ("mentee", re.compile(r"(мо\w+\s+стаж[её]р\w*|подопечн\w*)")),
    (
        "whois",
        re.compile(
            r"кто\s+(мой|моя|мне)\s+(наставник|руководител|куратор|бадди|buddy|hr)|как\s+связаться\s+с\s+(наставник|руководител|куратор|hr)|мои\s+контакт"
        ),
    ),
    (
        "escalate",
        re.compile(
            r"(спроси|передай|напиши|уточни)\w*\s+(\w+\s+)?(у\s+)?(наставник|руководител|куратор|олег|марии|hr)"
        ),
    ),
    (
        "help",
        re.compile(
            r"^(помощь|help|что\s+ты\s+(умеешь|можешь)|чем\s+(ты\s+)?(можешь\s+)?помо\w+|как\s+(тобой|с\s+тобой)\s+пользоваться|как\s+пользоваться\s+(ботом|buddy|бадди))\??$"
        ),
    ),
    (
        "smalltalk",
        re.compile(
            r"^(привет|здравствуй\w*|добрый\s+(день|вечер)|доброе\s+утро|спасибо|благодарю|ок|окей|понятно|пока|хорошо)[!. ]*$"
        ),
    ),
]
WRITE_INTENTS = frozenset({"absence", "access", "approvals", "trip", "consents"})
ESCALATE_VERB = re.compile(
    r"спроси|передай|напиши|уточни|позови|соедини|эскалир|нужен\s+человек|живого|нужна\s+помощь\s+человека|подготовь\s+сообщение"
)
KB_QUESTION_HINT = re.compile(
    r"^(как|где|что|какие|какой|какая|когда|зачем|почему|сколько|можно\s+ли|нужно\s+ли|кто)\b"
)


@dataclass
class Route:
    intent: str
    confidence: float
    source: str  # guardrail | rule | classifier | fallback | state
    reason: str = ""
    redirect_role: str = ""
    detail: dict = field(default_factory=dict)


def detect_other_person(original: str, own_name: str = "") -> str | None:
    """Return the detected foreign person (or 'colleague') when the user asks about another person's data/actions."""
    own_first = (own_name.split() or [""])[0][:4].lower()
    for rx in (OTHER_ACTION, OTHER_DATA):
        for m in rx.finditer(original):
            target = next((g for g in reversed(m.groups()) if g and g.strip()), "")
            word = target.split()[-1] if target else ""
            if word[:1].isupper():
                if word in NOT_NAMES or (own_first and word[:4].lower() == own_first):
                    continue
                return word
            return "colleague"
    return None


def guard(text: str, original: str, own_name: str = "") -> Route | None:
    if INJECTION.search(original):
        return Route("refusal", 1.0, "guardrail", reason="prompt_injection")
    if SENSITIVE.search(text):
        return Route("refusal", 1.0, "guardrail", reason="sensitive_data", redirect_role="hrbp")
    if HIRING_FIRING.search(text):
        return Route("refusal", 1.0, "guardrail", reason="out_of_scope", redirect_role="hrbp", detail={"topic": "hr"})
    if TRIP_OPS.search(text):
        return Route("refusal", 1.0, "guardrail", reason="out_of_scope", redirect_role="lead", detail={"topic": "trip"})
    if BENEFIT_OPS.search(text):  # no money operations, but explain how to do it yourself
        return Route("benefit", 1.0, "guardrail", detail={"topic": "benefit"})
    other = detect_other_person(original, own_name)
    if other:
        return Route("refusal", 1.0, "guardrail", reason="no_rights", detail={"person": other})
    if HUMAN_ONLY.search(text):
        return Route("human_only", 1.0, "guardrail")
    if DELEGATE_WORK.search(text):  # "сделай за меня" — coach, do not do the intern's work
        return Route("learning", 1.0, "guardrail")
    if looks_like_log(original):
        return Route("troubleshoot", 0.95, "rule")
    return None


class IntentRouter:
    def __init__(self, model: IntentModel | None, min_confidence: float = 0.4) -> None:
        self.model = model
        self.min_confidence = min_confidence

    def route(self, message: str, own_name: str = "") -> Route:
        text = normalize(message)
        if not text:
            return Route("help", 1.0, "rule")
        g = guard(text, message, own_name)
        if g:
            return g
        for intent, rx in RULES:
            if rx.search(text):
                if intent in ("absence", "access", "channels") and self._is_info_question(text, intent):
                    return Route("kb_question", 0.9, "rule", detail={"topic": intent})
                return Route(intent, 0.95, "rule")
        if self.model is not None:
            p = self.model.predict(text)
            if p.confidence >= self.min_confidence:
                if p.intent == "escalate" and not ESCALATE_VERB.search(text):
                    return Route("kb_question", p.confidence, "classifier", detail={"ranking": p.ranking})
                if p.intent in WRITE_INTENTS and p.confidence < 0.6:
                    # write scenarios start from the classifier only when it is confident; rules catch the usual phrasing
                    return Route("kb_question", p.confidence, "classifier", detail={"ranking": p.ranking})
                return Route(p.intent, p.confidence, "classifier", detail={"ranking": p.ranking})
        ranking = self.model.predict(text).ranking if self.model is not None else []
        return Route("kb_question", 0.3, "fallback", detail={"ranking": ranking})

    @staticmethod
    def _is_info_question(text: str, intent: str) -> bool:
        """«Сколько дней отпуска у стажёра?» → knowledge question, not a request flow."""
        if intent == "absence":
            return bool(re.search(r"(больничн|заболел)", text))
        if intent == "access":
            # «как склонировать репозиторий?» is engineering onboarding (KB), not an access request
            return bool(
                re.search(r"(клонир|ssh|ветк|pull\s+request|пулл?\s*реквест|коммит|запуш|структур)", text)
            ) and not (re.search(r"доступ|права\s+на|запроси|оформи|выдай", text))
        return False
