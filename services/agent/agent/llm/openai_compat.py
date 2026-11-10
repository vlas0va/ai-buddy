"""OpenAI-compatible chat completions client (cloud provider, vLLM, Ollama `http://ollama:11434/v1`)."""

from __future__ import annotations

import json
from collections.abc import Iterator
from dataclasses import dataclass, field
from typing import Any

import httpx

from ..errors import LLMError


@dataclass
class Completion:
    content: str = ""
    tool_calls: list[dict[str, Any]] = field(default_factory=list)


class OpenAICompatClient:
    def __init__(
        self,
        base_url: str,
        api_key: str,
        model: str,
        timeout_s: float = 30.0,
        temperature: float = 0.2,
        max_tokens: int = 700,
        client: httpx.Client | None = None,
    ) -> None:
        if not base_url or not model:
            raise ValueError("LLM_BASE_URL and LLM_MODEL are required for LLM_PROVIDER=openai_compat")
        self.url = base_url.rstrip("/") + "/chat/completions"
        self.model = model
        self.temperature = temperature
        self.max_tokens = max_tokens
        headers = {"Content-Type": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"
        self.client = client or httpx.Client(timeout=httpx.Timeout(timeout_s, connect=5.0))
        self.headers = headers

    def _body(self, messages: list[dict[str, Any]], stream: bool, tools: list[dict[str, Any]] | None) -> dict[str, Any]:
        body: dict[str, Any] = {
            "model": self.model,
            "messages": messages,
            "temperature": self.temperature,
            "max_tokens": self.max_tokens,
            "stream": stream,
        }
        if tools:
            body["tools"] = tools
            body["tool_choice"] = "auto"
        return body

    @staticmethod
    def _raise_for(resp: httpx.Response) -> None:
        if resp.status_code >= 400:
            retryable = resp.status_code in (408, 429) or resp.status_code >= 500
            raise LLMError(f"Модель вернула ошибку {resp.status_code}.", retryable=retryable)

    def complete(self, messages: list[dict[str, Any]], tools: list[dict[str, Any]] | None = None) -> Completion:
        try:
            resp = self.client.post(self.url, json=self._body(messages, False, tools), headers=self.headers)
        except httpx.TimeoutException as exc:
            raise LLMError("Модель не ответила вовремя.", code="llm_timeout") from exc
        except httpx.HTTPError as exc:
            raise LLMError("Модель недоступна.") from exc
        self._raise_for(resp)
        msg = (resp.json().get("choices") or [{}])[0].get("message", {})
        return Completion(content=msg.get("content") or "", tool_calls=msg.get("tool_calls") or [])

    def stream(self, messages: list[dict[str, Any]]) -> Iterator[str]:
        try:
            with self.client.stream(
                "POST", self.url, json=self._body(messages, True, None), headers=self.headers
            ) as resp:
                if resp.status_code >= 400:
                    resp.read()
                    self._raise_for(resp)
                for line in resp.iter_lines():
                    if not line.startswith("data:"):
                        continue
                    data = line[5:].strip()
                    if data == "[DONE]":
                        break
                    try:
                        chunk = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    delta = (chunk.get("choices") or [{}])[0].get("delta", {})
                    if delta.get("content"):
                        yield delta["content"]
        except httpx.TimeoutException as exc:
            raise LLMError("Модель не ответила вовремя.", code="llm_timeout") from exc
        except httpx.HTTPError as exc:
            raise LLMError("Модель недоступна.") from exc


class EmbeddingsClient:
    """OpenAI-compatible /embeddings (e.g. Ollama `bge-m3`, `nomic-embed-text`) for optional hybrid retrieval."""

    def __init__(
        self, base_url: str, api_key: str, model: str, timeout_s: float = 30.0, client: httpx.Client | None = None
    ) -> None:
        self.url = base_url.rstrip("/") + "/embeddings"
        self.model = model
        self.headers = {"Authorization": f"Bearer {api_key}"} if api_key else {}
        self.client = client or httpx.Client(timeout=timeout_s)

    def __call__(self, texts: list[str]) -> list[list[float]]:
        out: list[list[float]] = []
        for i in range(0, len(texts), 32):
            resp = self.client.post(
                self.url, json={"model": self.model, "input": texts[i : i + 32]}, headers=self.headers
            )
            resp.raise_for_status()
            data = sorted(resp.json()["data"], key=lambda d: d.get("index", 0))
            out.extend(d["embedding"] for d in data)
        return out
