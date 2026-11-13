"""Канонический JSON, хеширование аргументов и токены подтверждения (contracts/agent-stream.md, раздел 4)."""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time
from dataclasses import dataclass
from typing import Any

CONFIRMATION_TTL_S = 300


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def args_hash(args: dict[str, Any]) -> str:
    clean = {k: v for k, v in args.items() if k not in ("idempotency_key", "confirmation_token")}
    return hashlib.sha256(canonical_json(clean).encode("utf-8")).hexdigest()


def _b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def _unb64(data: str) -> bytes:
    pad = "=" * (-len(data) % 4)
    return base64.urlsafe_b64decode(data + pad)


def _sign(secret: str, payload_b64: str) -> str:
    return _b64(hmac.new(secret.encode("utf-8"), payload_b64.encode("ascii"), hashlib.sha256).digest())


def create_confirmation_token(
    secret: str,
    user_id: str,
    proposal_id: str,
    hash_hex: str,
    now: float | None = None,
    ttl_s: int = CONFIRMATION_TTL_S,
) -> str:
    """Формат из contracts/mcp-tools.md: base64url(json{sub,key,args_hash,exp}) + "." + base64url(HMAC-SHA256(secret,
    part1)).

    Ядро выпускает его при подтверждении; агент использует этот помощник только в режиме разработки и в тестах.
    """
    exp = int((now if now is not None else time.time()) + ttl_s)
    payload = {"sub": user_id, "key": proposal_id, "args_hash": hash_hex, "exp": exp}
    payload_b64 = _b64(canonical_json(payload).encode("utf-8"))
    return f"{payload_b64}.{_sign(secret, payload_b64)}"


@dataclass(frozen=True)
class TokenCheck:
    ok: bool
    reason: str = ""


def verify_confirmation_token(
    secret: str, token: str, user_id: str, proposal_id: str, hash_hex: str, now: float | None = None
) -> TokenCheck:
    parts = (token or "").split(".")
    if len(parts) != 2 or not parts[0] or not parts[1]:
        return TokenCheck(False, "malformed")
    if not hmac.compare_digest(_sign(secret, parts[0]), parts[1]):
        return TokenCheck(False, "bad_signature")
    try:
        payload = json.loads(_unb64(parts[0]))
    except (ValueError, json.JSONDecodeError):
        return TokenCheck(False, "malformed")
    current = now if now is not None else time.time()
    if int(payload.get("exp", 0)) < current:
        return TokenCheck(False, "expired")
    if payload.get("sub") != user_id:
        return TokenCheck(False, "foreign_user")
    if payload.get("key") != proposal_id:
        return TokenCheck(False, "foreign_proposal")
    if payload.get("args_hash") != hash_hex:
        return TokenCheck(False, "hash_mismatch")
    return TokenCheck(True)
