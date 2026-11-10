"""Unit tests for building blocks: canonical tokens, dates, retrieval, state, config, metrics, mocks."""

from __future__ import annotations

from datetime import date
from pathlib import Path

import pytest

from agent.canonical import args_hash, canonical_json, create_confirmation_token, verify_confirmation_token
from agent.config import load_agent_config, substitute_env
from agent.dates import add_working_days, human_range, is_working_day, parse_range, working_days
from agent.errors import ToolDenied, ToolError, ToolValidationError
from agent.metrics import Metrics
from agent.retrieval import KnowledgeBase, parse_doc
from agent.sse import Event, encode, parse_stream, status
from agent.state import MemoryStore, RedisStore, make_store
from agent.text import name_stem, same_person

TODAY = date(2026, 11, 9)


# --------------------------------------------------------------------------- canonical / confirmation tokens
def test_CT_01_canonical_json_and_hash_ignore_service_fields():
    assert canonical_json({"b": 1, "a": "я"}) == '{"a":"я","b":1}'
    assert args_hash({"a": 1, "idempotency_key": "x", "confirmation_token": "y"}) == args_hash({"a": 1})


@pytest.mark.parametrize(
    "case,kwargs,reason",
    [
        ("ok", {}, ""),
        ("expired", {"now_verify": 10_000}, "expired"),
        ("foreign_user", {"user": "intern.ivan"}, "foreign_user"),
        ("foreign_proposal", {"pid": "other"}, "foreign_proposal"),
        ("hash", {"hash": "0" * 64}, "hash_mismatch"),
    ],
)
def test_CT_02_token_verification_matrix(case, kwargs, reason):
    tok = create_confirmation_token("s", "intern.anna", "p1", "h" * 64, now=0)
    check = verify_confirmation_token(
        "s",
        tok,
        kwargs.get("user", "intern.anna"),
        kwargs.get("pid", "p1"),
        kwargs.get("hash", "h" * 64),
        now=kwargs.get("now_verify", 1),
    )
    assert check.ok == (case == "ok") and check.reason == reason


def test_CT_03_malformed_and_bad_signature():
    assert verify_confirmation_token("s", "abc", "u", "p", "h").reason == "malformed"
    tok = create_confirmation_token("s", "u", "p", "h")
    assert verify_confirmation_token("other", tok, "u", "p", "h").reason == "bad_signature"
    part = tok.split(".")[0]
    import base64
    import hashlib
    import hmac

    bad_payload = "!!!"
    sig = base64.urlsafe_b64encode(hmac.new(b"s", bad_payload.encode(), hashlib.sha256).digest()).rstrip(b"=").decode()
    assert verify_confirmation_token("s", f"{bad_payload}.{sig}", "u", "p", "h").reason == "malformed"
    assert part


# --------------------------------------------------------------------------- dates (S3-02)
@pytest.mark.parametrize(
    "text,start,end",
    [
        ("в пятницу", "2026-11-13", "2026-11-13"),
        ("завтра", "2026-11-10", "2026-11-10"),
        ("послезавтра", "2026-11-11", "2026-11-11"),
        ("14.11", "2026-11-14", "2026-11-14"),
        ("20.11.2026", "2026-11-20", "2026-11-20"),
        ("16 ноября", "2026-11-16", "2026-11-16"),
        ("с 16 по 18 ноября", "2026-11-16", "2026-11-18"),
        ("16-18 ноября", "2026-11-16", "2026-11-18"),
        ("с понедельника по среду", "2026-11-16", "2026-11-18"),
        ("в следующий вторник", "2026-11-17", "2026-11-17"),
        ("с 16.11 по 18.11", "2026-11-16", "2026-11-18"),
        ("на 2 дня с 16 ноября", "2026-11-16", "2026-11-17"),
        ("через 3 дня", "2026-11-12", "2026-11-12"),
        ("через неделю", "2026-11-16", "2026-11-16"),
        ("на неделю с понедельника", "2026-11-16", "2026-11-20"),
        ("1 января", "2027-01-01", "2027-01-01"),
        ("с пятницы по понедельник", "2026-11-13", "2026-11-16"),
    ],
)
def test_S3_02_parse_russian_dates(text, start, end):
    r = parse_range(text, TODAY)
    assert r is not None and (r.start.isoformat(), r.end.isoformat()) == (start, end)


def test_S3_02b_calendar_helpers():
    assert parse_range("просто текст", TODAY) is None
    assert not is_working_day(date(2026, 11, 4)) and not is_working_day(date(2026, 11, 14))
    assert working_days(date(2026, 11, 13), date(2026, 11, 16)) == 2
    assert working_days(date(2026, 11, 16), date(2026, 11, 13)) == 0
    assert add_working_days(date(2026, 11, 14), 1) == date(2026, 11, 16)
    assert human_range(parse_range("с 16 по 17 ноября", TODAY)) == "с 16 ноября (пн) по 17 ноября (вт)"
    assert parse_range("31.02", TODAY) is None


# --------------------------------------------------------------------------- retrieval
def test_S2_15_retrieval_acl_before_ranking_and_snippets():
    kb = KnowledgeBase.from_dir(Path(__file__).resolve().parents[3] / "data" / "kb")
    assert all(d.source_type == "test" for d in kb.docs) and len(kb.docs) >= 30
    hits = kb.search("методичка наставника", "intern", "delivery")
    assert "mentor-handbook" not in [h.doc.doc_id for h in hits]
    assert kb.search("методичка наставника", "mentor", "delivery")[0].doc.doc_id == "mentor-handbook"
    assert kb.search("", "intern", "delivery") == []
    assert kb.get("mentor-handbook", "intern", "delivery") is None


def test_S2_16_kb_rejects_non_test_documents(tmp_path):
    (tmp_path / "x.md").write_text(
        "---\ndoc_id: x\nsource_type: official\nacl: {roles: [intern], teams: ['*']}\n---\nТекст"
    )
    with pytest.raises(ValueError):
        KnowledgeBase.from_dir(tmp_path)


def test_S2_17_hybrid_retrieval_with_embeddings(tmp_path):
    docs = [
        parse_doc("---\ndoc_id: a\ntitle: Ревью кода\nacl: {roles: [intern], teams: ['*']}\n---\nКак проходит ревью"),
        parse_doc("---\ndoc_id: b\ntitle: Отгул\nacl: {roles: [intern], teams: ['*']}\n---\nКак оформить отгул"),
    ]

    def embed(texts):  # noqa: ANN001, ANN202
        return [[1.0, 0.0] if ("ревью" in t.lower() or "код" in t.lower()) else [0.0, 1.0] for t in texts]

    kb = KnowledgeBase(docs, embedder=embed)
    assert kb.hybrid and kb.search("ревью кода", "intern", "x")[0].doc.doc_id == "a"

    def broken(texts):  # noqa: ANN001, ANN202
        raise RuntimeError("down")

    assert not KnowledgeBase(docs, embedder=broken).hybrid


# --------------------------------------------------------------------------- sse, state, config, metrics, text
def test_AG_16_sse_roundtrip_and_status_enum():
    raw = encode(Event("token", {"text": "привет"}), 1) + encode(status("composing", "Формулирую"), 2)
    ev = parse_stream(raw)
    assert ev[0].data["text"] == "привет" and ev[1].data["stage"] == "thinking"
    with pytest.raises(ValueError):
        Event("nope", {})


def test_AG_17_memory_and_redis_stores():
    now = [0.0]
    m = MemoryStore(clock=lambda: now[0])
    m.set("k", {"a": 1}, ttl_s=10)
    assert m.get("k") == {"a": 1}
    now[0] = 20
    assert m.get("k") is None
    m.set("k", {"a": 1})
    m.delete("k")
    assert m.get("missing") is None

    class FakeRedis(dict):
        def set(self, k, v, ex=None):  # noqa: ANN001, ANN202
            self[k] = v

        def delete(self, k):  # noqa: ANN001, ANN202
            self.pop(k, None)

    r = RedisStore(FakeRedis())
    r.set("x", {"b": 2})
    assert r.get("x") == {"b": 2}
    r.delete("x")
    assert r.get("x") is None
    assert isinstance(make_store(""), MemoryStore)
    assert isinstance(make_store("redis://127.0.0.1:1/0"), MemoryStore)  # unreachable → memory fallback


def test_AG_18_config_env_substitution_and_validation(tmp_path):
    assert substitute_env("${A:-x}/${B}", {"A": "1"}) == "1/"
    cfg = load_agent_config(env={"LLM_PROVIDER": "openai_compat", "MCP_KB_URL": "http://kb/mcp"})
    assert cfg.llm["provider"] == "openai_compat" and cfg.mcp_servers["kb"]["url"] == "http://kb/mcp"
    assert cfg.intent_allowed("unknown", "intern") and cfg.intent_flag("unknown") is None
    bad = tmp_path / "agent.yaml"
    bad.write_text("prompt: {system: x}\ntools:\n  - {name: a.b, roles: [intern], write: true}\n")
    with pytest.raises(ValueError):
        load_agent_config(bad)


def test_AG_19_metrics_render():
    m = Metrics()
    m.inc("x_total", kind="a")
    m.inc("plain")
    m.observe("lat", 0.3)
    out = m.render()
    assert 'x_total{kind="a"} 1.0' in out and "plain 1.0" in out and 'lat_bucket{le="0.5"} 1' in out


def test_AG_20_name_matching():
    assert name_stem("Анны") == "анн" and same_person("Анне", "Анна Котова") and not same_person("Иван", "Анна Котова")


def test_AG_21_errors_to_dict():
    assert ToolError().to_dict()["retryable"] is True
    assert ToolValidationError("x").code == "validation_failed" and ToolDenied().http_status == 403


def test_S2_22_kb_rejects_documents_without_acl(tmp_path):
    (tmp_path / "x.md").write_text("# Без frontmatter\nТекст", encoding="utf-8")
    with pytest.raises(ValueError):
        KnowledgeBase.from_dir(tmp_path)
