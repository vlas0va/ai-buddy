"""Состояние диалога по сессиям и реестр черновиков: Redis, если настроен, иначе память."""

from __future__ import annotations

import json
import threading
import time
from typing import Any, Protocol


class StateStore(Protocol):
    def get(self, key: str) -> dict[str, Any] | None: ...
    def set(self, key: str, value: dict[str, Any], ttl_s: int = 3600) -> None: ...
    def delete(self, key: str) -> None: ...


class MemoryStore:
    def __init__(self, clock: Any = time.time) -> None:
        self._data: dict[str, tuple[float, str]] = {}
        self._lock = threading.Lock()
        self._clock = clock

    def get(self, key: str) -> dict[str, Any] | None:
        with self._lock:
            item = self._data.get(key)
            if not item:
                return None
            exp, raw = item
            if exp < self._clock():
                self._data.pop(key, None)
                return None
            return json.loads(raw)

    def set(self, key: str, value: dict[str, Any], ttl_s: int = 3600) -> None:
        with self._lock:
            self._data[key] = (self._clock() + ttl_s, json.dumps(value, ensure_ascii=False))

    def delete(self, key: str) -> None:
        with self._lock:
            self._data.pop(key, None)


class RedisStore:
    def __init__(self, client: Any, prefix: str = "buddy:agent:") -> None:
        self._r = client
        self._p = prefix

    def get(self, key: str) -> dict[str, Any] | None:
        raw = self._r.get(self._p + key)
        return json.loads(raw) if raw else None

    def set(self, key: str, value: dict[str, Any], ttl_s: int = 3600) -> None:
        self._r.set(self._p + key, json.dumps(value, ensure_ascii=False), ex=ttl_s)

    def delete(self, key: str) -> None:
        self._r.delete(self._p + key)


def make_store(redis_url: str) -> StateStore:
    if redis_url:
        try:
            import redis  # type: ignore[import-not-found]

            client = redis.Redis.from_url(redis_url, socket_timeout=1)
            client.ping()
            return RedisStore(client)
        except Exception:  # noqa: BLE001 - fall back to memory, logged by caller
            return MemoryStore()
    return MemoryStore()
