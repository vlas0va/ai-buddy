"""SSE event model and wire encoding (contracts/agent-stream.md, section 3)."""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any

TERMINAL = frozenset({"done", "error"})
EVENT_NAMES = frozenset(
    {
        "status",
        "token",
        "sources",
        "action_proposal",
        "action_result",
        "escalation_draft",
        "next_step",
        "date_picker",  # feedback 12.11: a calendar to pick absence dates
        "quiz_passed",  # organizers 12.13: a full quiz score confirms a study task of the plan (core marks it)
        "refusal",
        "tool_result",
        "error",
        "done",
    }
)
STATUS_TEXT = {
    "thinking": "Думаю",
    "searching_kb": "Ищу в базе знаний",
    "calling_service": "Обращаюсь к сервису",
    "composing": "Формулирую ответ",
    "saving": "Сохраняю",
}


@dataclass(frozen=True)
class Event:
    name: str
    data: Any

    def __post_init__(self) -> None:
        if self.name not in EVENT_NAMES:
            raise ValueError(f"unknown event {self.name}")


STAGES = frozenset({"thinking", "searching_kb", "calling_service", "saving"})  # enum of contracts/agent-stream.md


def status(stage: str, text: str | None = None) -> Event:
    label = text or STATUS_TEXT.get(stage, stage)
    return Event("status", {"stage": stage if stage in STAGES else "thinking", "text": label})


def token(text: str) -> Event:
    return Event("token", {"text": text})


def encode(event: Event, seq: int) -> str:
    payload = json.dumps(event.data, ensure_ascii=False, separators=(",", ":"))
    return f"id: {seq}\nevent: {event.name}\ndata: {payload}\n\n"


def parse_stream(raw: str) -> list[Event]:
    """Parse an SSE body back into events (tests, eval runner, dev tools)."""
    events: list[Event] = []
    for block in raw.strip().split("\n\n"):
        name, data = None, []
        for line in block.splitlines():
            if line.startswith("event: "):
                name = line[7:]
            elif line.startswith("data: "):
                data.append(line[6:])
        if name:
            events.append(Event(name, json.loads("\n".join(data))))
    return events
