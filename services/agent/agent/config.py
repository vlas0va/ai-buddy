"""Настройки из окружения и декларативного agent.yaml (R53)."""

from __future__ import annotations

import os
import re
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

SERVICE_ROOT = Path(__file__).resolve().parent.parent
REPO_ROOT = SERVICE_ROOT.parent.parent

_ENV_RE = re.compile(r"\$\{([A-Z0-9_]+)(?::-([^}]*))?\}")


def _truthy(value: Any) -> bool:
    return str(value).strip().lower() in ("1", "true", "yes", "on")


def substitute_env(text: str, env: dict[str, str] | None = None) -> str:
    env = os.environ if env is None else env

    def repl(m: re.Match[str]) -> str:
        value = env.get(m.group(1))
        return value if value not in (None, "") else (m.group(2) or "")

    return _ENV_RE.sub(repl, text)


@dataclass(frozen=True)
class ToolSpec:
    name: str
    roles: tuple[str, ...]
    write: bool
    description: str = ""
    input_schema: dict[str, Any] = field(default_factory=dict)
    proposal_kind: str | None = None

    @property
    def server(self) -> str:
        return self.name.split(".", 1)[0]


@dataclass(frozen=True)
class AgentConfig:
    raw: dict[str, Any]
    tools: dict[str, ToolSpec]
    base_dir: Path

    @property
    def prompt_version(self) -> str:
        return str(self.raw.get("prompt", {}).get("version", "unknown"))

    def prompt_text(self, key: str) -> str:
        rel = self.raw["prompt"][key]
        return (self.base_dir / rel).read_text(encoding="utf-8")

    @property
    def llm(self) -> dict[str, Any]:
        return self.raw.get("llm", {})

    @property
    def retrieval(self) -> dict[str, Any]:
        return self.raw.get("retrieval", {})

    @property
    def embeddings(self) -> dict[str, Any]:
        return self.raw.get("embeddings", {}) or {}

    @property
    def nlu(self) -> dict[str, Any]:
        return self.raw.get("nlu", {})

    @property
    def mcp_servers(self) -> dict[str, dict[str, Any]]:
        return self.raw.get("mcp_servers", {})

    @property
    def intents(self) -> dict[str, dict[str, Any]]:
        return self.raw.get("intents", {})

    def tools_for_role(self, role: str, include_write: bool = False) -> list[ToolSpec]:
        return [t for t in self.tools.values() if role in t.roles and (include_write or not t.write)]

    def is_allowed(self, tool: str, role: str) -> bool:
        spec = self.tools.get(tool)
        return bool(spec and role in spec.roles)

    def intent_allowed(self, intent: str, role: str) -> bool:
        rule = self.intents.get(intent)
        return True if rule is None else role in rule.get("roles", [])

    def intent_flag(self, intent: str) -> str | None:
        rule = self.intents.get(intent)
        return rule.get("flag") if rule else None

    def sanitized(self, role: str) -> dict[str, Any]:
        return {
            "name": self.raw.get("name"),
            "prompt_version": self.prompt_version,
            "provider": self.llm.get("provider"),
            "model": self.llm.get("model") or None,
            "tool_calling": _truthy(self.llm.get("tool_calling")),
            "role": role,
            "read_tools": [t.name for t in self.tools_for_role(role)],
            "write_tools_via_confirmation": [t.name for t in self.tools_for_role(role, True) if t.write],
            "mcp_servers": {
                k: ("http" if (v.get("url") or os.environ.get("MCP_BASE_URL")) else "inprocess")
                for k, v in self.mcp_servers.items()
            },
        }


LLM_DEFAULTABLE = ("PROVIDER", "BASE_URL", "MODEL", "TIMEOUT_S")


def with_llm_defaults(env: Mapping[str, str]) -> dict[str, str]:
    """LLM_<X> из .env побеждает; если он не задан или пуст, применяется LLM_DEFAULT_<X>. Compose ставит по умолчанию
    A-Vibe во встроенной Ollama, поэтому эксперт получает модель вообще без .env, а строка в .env всё равно её
    переопределяет.
    """
    out = dict(env)
    for key in LLM_DEFAULTABLE:
        if not out.get(f"LLM_{key}") and out.get(f"LLM_DEFAULT_{key}"):
            out[f"LLM_{key}"] = out[f"LLM_DEFAULT_{key}"]
    return out


def load_agent_config(path: str | os.PathLike[str] | None = None, env: dict[str, str] | None = None) -> AgentConfig:
    cfg_path = Path(path or os.environ.get("AGENT_CONFIG") or SERVICE_ROOT / "agent.yaml")
    merged = with_llm_defaults(os.environ if env is None else env)
    raw = yaml.safe_load(substitute_env(cfg_path.read_text(encoding="utf-8"), merged))
    tools = {
        t["name"]: ToolSpec(
            name=t["name"],
            roles=tuple(t.get("roles", [])),
            write=bool(t.get("write", False)),
            description=t.get("description", ""),
            input_schema=t.get("input_schema", {}) or {},
            proposal_kind=t.get("proposal_kind"),
        )
        for t in raw.get("tools", [])
    }
    for spec in tools.values():
        if spec.write and spec.proposal_kind is None:
            raise ValueError(f"write tool {spec.name} must declare proposal_kind")
    return AgentConfig(raw=raw, tools=tools, base_dir=cfg_path.parent)


@dataclass
class Settings:
    jwt_secret: str = "change-me-local-only"
    confirmation_secret: str = "change-me-local-only"
    service_token: str = ""
    redis_url: str = ""
    kb_dir: Path = REPO_ROOT / "data" / "kb"
    checklist_path: Path = REPO_ROOT / "data" / "checklist" / "checklist.yaml"
    fixtures_path: Path = SERVICE_ROOT / "fixtures" / "mock_services.yaml"
    dev_mode: bool = False
    allow_clock_override: bool = False
    mock_faults: str = ""
    mcp_base_url: str = ""
    llm_api_key: str = ""
    embeddings_api_key: str = ""
    log_level: str = "INFO"

    @classmethod
    def from_env(cls, env: dict[str, str] | None = None) -> Settings:
        e = os.environ if env is None else env
        d = cls()
        return cls(
            jwt_secret=e.get("JWT_SECRET", d.jwt_secret),
            confirmation_secret=e.get("CONFIRMATION_HMAC_SECRET", d.confirmation_secret),
            service_token=e.get("AGENT_SERVICE_TOKEN", ""),
            redis_url=e.get("REDIS_URL", "") if _truthy(e.get("AGENT_USE_REDIS", "false")) else "",
            kb_dir=Path(e.get("KB_DIR", str(d.kb_dir))),
            checklist_path=Path(e.get("CHECKLIST_PATH", str(d.checklist_path))),
            fixtures_path=Path(e.get("MOCK_FIXTURES_PATH", str(d.fixtures_path))),
            dev_mode=_truthy(e.get("AGENT_DEV_MODE", "false")),
            allow_clock_override=_truthy(e.get("AGENT_ALLOW_CLOCK_OVERRIDE", "false")),
            mock_faults=e.get("MOCK_FAULTS", ""),
            mcp_base_url=e.get("MCP_BASE_URL", ""),
            llm_api_key=e.get("LLM_API_KEY", ""),
            embeddings_api_key=e.get("EMBEDDINGS_API_KEY", "") or e.get("LLM_API_KEY", ""),
            log_level=e.get("LOG_LEVEL", "INFO"),
        )


truthy = _truthy
