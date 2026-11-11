"""MCP over streamable HTTP and the OpenAI-compatible provider, tested with httpx.MockTransport (no network)."""

from __future__ import annotations

import json

import httpx
import pytest

from agent.config import load_agent_config
from agent.errors import LLMError, ToolDenied, ToolError, ToolValidationError
from agent.llm.openai_compat import EmbeddingsClient, OpenAICompatClient, ThinkFilter, strip_think
from agent.llm.writer import LLMWriter, StubWriter, make_writer
from agent.orchestrator import build_agent
from agent.tools.mcp_client import McpHttpServer
from tests.conftest import Client, first, make_settings, text_of


def mcp_transport(results: dict, calls: list, status_seq: list[int] | None = None):  # noqa: ANN201
    status_seq = list(status_seq or [])

    def handler(req: httpx.Request) -> httpx.Response:
        body = json.loads(req.content)
        calls.append((body.get("method"), req.headers.get("Authorization"), req.headers.get("Mcp-Session-Id")))
        if body["method"] == "initialize":
            return httpx.Response(
                200, json={"jsonrpc": "2.0", "id": body["id"], "result": {}}, headers={"Mcp-Session-Id": "sess-1"}
            )
        if body["method"] == "notifications/initialized":
            return httpx.Response(202)
        if status_seq:
            code = status_seq.pop(0)
            if code != 200:
                return httpx.Response(code, text="err")
        name = body["params"]["name"]
        res = results[name]
        if res == "sse":
            data = json.dumps({"jsonrpc": "2.0", "id": body["id"], "result": {"structuredContent": {"items": [1]}}})
            return httpx.Response(
                200, text=f"event: message\ndata: {data}\n\n", headers={"content-type": "text/event-stream"}
            )
        return httpx.Response(200, json={"jsonrpc": "2.0", "id": body["id"], "result": res})

    return httpx.MockTransport(handler)


def test_MCP_11_handshake_token_forwarding_and_results():
    calls: list = []
    results = {
        "whoami": {"structuredContent": {"employee_id": "intern.anna"}},
        "search": "sse",
        "get_policy": {"content": [{"type": "text", "text": json.dumps({"doc_id": "d"})}]},
        "plain": {"content": [{"type": "text", "text": "not json"}]},
    }
    s = McpHttpServer("directory", "http://mcp/mcp", client=httpx.Client(transport=mcp_transport(results, calls)))
    assert s.call("directory.whoami", {}, "TOKEN")["employee_id"] == "intern.anna"
    assert s.call("kb.search", {"query": "x"}, "TOKEN") == {"items": [1]}
    assert s.call("absence.get_policy", {}, "TOKEN") == {"doc_id": "d"}
    assert s.call("x.plain", {}, "TOKEN") == "not json"
    assert calls[0][0] == "initialize" and all(c[1] == "Bearer TOKEN" for c in calls)
    assert calls[2][2] == "sess-1"  # session id sent after the handshake


@pytest.mark.parametrize(
    "payload,exc,code",
    [
        ({"code": "forbidden", "message": "Нет"}, ToolDenied, "no_rights"),
        ({"code": "invalid_confirmation", "message": "Плохо"}, ToolDenied, "confirmation_invalid"),
        ({"code": "conflict", "message": "Уже"}, ToolValidationError, "conflict"),
        ({"code": "validation_error", "message": "Дата"}, ToolValidationError, "validation_failed"),
        ({"code": "unavailable", "message": "Лежит", "retryable": True}, ToolError, "service_unavailable"),
    ],
)
def test_MCP_12_tool_errors_follow_contract(payload, exc, code):
    results = {"t": {"isError": True, "structuredContent": payload}}
    s = McpHttpServer("x", "http://mcp/mcp", client=httpx.Client(transport=mcp_transport(results, [])))
    with pytest.raises(exc) as e:
        s.call("x.t", {}, "T")
    assert e.value.code == code and e.value.message == payload["message"]


def test_MCP_13_http_failures_and_session_reinit():
    calls: list = []
    ok = {"t": {"structuredContent": {"ok": True}}, "e": {"isError": True, "content": [{"type": "text", "text": "{}"}]}}
    s = McpHttpServer("x", "http://mcp/mcp", client=httpx.Client(transport=mcp_transport(ok, calls, [404, 200])))
    assert s.call("x.t", {}, "T") == {"ok": True}
    assert [c[0] for c in calls].count("initialize") == 2  # re-handshake after 404
    with pytest.raises(ToolValidationError):
        s.call("x.e", {}, "T")
    for code, exc in ((401, ToolDenied), (503, ToolError)):
        srv = McpHttpServer("x", "http://mcp/mcp", client=httpx.Client(transport=mcp_transport(ok, [], [code])))
        with pytest.raises(exc):
            srv.call("x.t", {}, "T")

    def timeout(req):  # noqa: ANN001, ANN202
        raise httpx.ReadTimeout("slow")

    srv = McpHttpServer("x", "http://mcp/mcp", client=httpx.Client(transport=httpx.MockTransport(timeout)))
    with pytest.raises(ToolError) as e:
        srv.call("x.t", {}, "T")
    assert e.value.code == "service_timeout"

    def jsonrpc_error(req):  # noqa: ANN001, ANN202
        body = json.loads(req.content)
        if body["method"] == "tools/call":
            return httpx.Response(200, json={"jsonrpc": "2.0", "id": body["id"], "error": {"message": "boom"}})
        return httpx.Response(200, json={"jsonrpc": "2.0", "id": 1, "result": {}})

    srv = McpHttpServer("x", "http://mcp/mcp", client=httpx.Client(transport=httpx.MockTransport(jsonrpc_error)))
    with pytest.raises(ToolError):
        srv.call("x.t", {}, "T")


def test_MCP_14_agent_uses_http_backend_when_url_set(monkeypatch):
    monkeypatch.setenv("MCP_KB_URL", "http://mcp-kb:8106/mcp")
    calls: list = []
    results = {
        "search": {
            "structuredContent": {
                "items": [
                    {
                        "doc_id": "delivery-code-review",
                        "title": "Ревью",
                        "snippet": "Каждый MR ревьюит наставник.",
                        "source_type": "test",
                        "url": "/kb/delivery-code-review",
                        "score": 7.0,
                    },
                    {
                        "doc_id": "mentor-handbook",
                        "title": "Методичка",
                        "snippet": "секрет",
                        "source_type": "test",
                        "score": 9.0,
                    },
                ]
            }
        }
    }
    settings = make_settings()
    agent = build_agent(
        settings, load_agent_config(), http_client=httpx.Client(transport=mcp_transport(results, calls))
    )
    from agent.app import create_app

    c = Client(create_app(settings, load_agent_config(), agent))
    s = c.login("intern.anna")
    ev = s.say("как проходит код-ревью")
    ids = [x["doc_id"] for x in first(ev, "sources")["items"]]
    assert ids == ["delivery-code-review"]  # agent-side ACL re-check drops the mentor-only doc (defense in depth)
    assert any(cl[0] == "tools/call" for cl in calls)


# --------------------------------------------------------------------------- LLM provider
def llm_transport(stream_chunks=None, complete_msgs=None, status: int = 200):  # noqa: ANN001, ANN201
    complete_msgs = list(complete_msgs or [])

    def handler(req: httpx.Request) -> httpx.Response:
        body = json.loads(req.content)
        if status != 200:
            return httpx.Response(status, text="err")
        if body.get("stream"):
            lines = [f"data: {json.dumps({'choices': [{'delta': {'content': c}}]})}" for c in stream_chunks or []]
            lines += ["data: {broken", "data: [DONE]"]
            return httpx.Response(200, text="\n\n".join(lines), headers={"content-type": "text/event-stream"})
        msg = complete_msgs.pop(0) if complete_msgs else {"content": "ok"}
        return httpx.Response(200, json={"choices": [{"message": msg}]})

    return httpx.MockTransport(handler)


def test_LLM_01_stream_and_complete():
    c = OpenAICompatClient("http://llm/v1", "key", "m", client=httpx.Client(transport=llm_transport(["При", "вет"])))
    assert "".join(c.stream([{"role": "user", "content": "x"}])) == "Привет"
    assert c.complete([], tools=[{"type": "function"}]).content == "ok"
    with pytest.raises(ValueError):
        OpenAICompatClient("", "", "")


def test_LLM_05_avibe_reasoning_is_never_shown():
    """A-Vibe (Qwen3) may answer with <think>…</think>: the block is cut out, also when a tag is split between chunks;
    with no_think the request asks the model not to reason."""
    seen: list[dict] = []
    chunks = ["<thi", "nk>план ответа", "</th", "ink>\n\nПри", "вет <", "b>"]
    inner = llm_transport(chunks, [{"content": "<think>x</think>Ответ"}])

    def spy(req: httpx.Request) -> httpx.Response:
        seen.append(json.loads(req.content))
        return inner.handle_request(req)

    c = OpenAICompatClient(
        "http://llm/v1", "", "m", client=httpx.Client(transport=httpx.MockTransport(spy)), no_think=True
    )
    assert "".join(c.stream([{"role": "system", "content": "s"}, {"role": "user", "content": "x"}])) == "Привет <b>"
    assert c.complete([{"role": "system", "content": "s"}]).content == "Ответ"
    assert seen[0]["messages"][0]["content"].endswith("/no_think")
    assert seen[0]["chat_template_kwargs"] == {"enable_thinking": False}
    unfinished = ThinkFilter()
    assert unfinished.feed("<think>долго думаю") == "" and unfinished.flush() == ""
    assert strip_think("<think>a</think> b") == "b"


@pytest.mark.parametrize("status,retryable", [(429, True), (500, True), (400, False)])
def test_LLM_02_http_errors(status, retryable):
    c = OpenAICompatClient("http://llm/v1", "", "m", client=httpx.Client(transport=llm_transport(status=status)))
    with pytest.raises(LLMError) as e:
        list(c.stream([]))
    assert e.value.retryable is retryable
    with pytest.raises(LLMError):
        c.complete([])


def test_LLM_03_timeouts():
    def slow(req):  # noqa: ANN001, ANN202
        raise httpx.ReadTimeout("slow")

    def down(req):  # noqa: ANN001, ANN202
        raise httpx.ConnectError("down")

    for fn, code in ((slow, "llm_timeout"), (down, "llm_unavailable")):
        c = OpenAICompatClient("http://llm/v1", "", "m", client=httpx.Client(transport=httpx.MockTransport(fn)))
        with pytest.raises(LLMError) as e:
            list(c.stream([]))
        assert e.value.code == code
        with pytest.raises(LLMError):
            c.complete([])


def llm_app(monkeypatch, transport, **env):  # noqa: ANN001, ANN201
    monkeypatch.setenv("LLM_PROVIDER", "openai_compat")
    monkeypatch.setenv("LLM_BASE_URL", "http://llm/v1")
    monkeypatch.setenv("LLM_MODEL", "test-model")
    for k, v in env.items():
        monkeypatch.setenv(k, v)
    settings = make_settings()
    cfg = load_agent_config()
    from agent.app import create_app

    return Client(create_app(settings, cfg, build_agent(settings, cfg, llm_client=httpx.Client(transport=transport))))


def test_S2_18_llm_answer_is_streamed_with_same_events(monkeypatch):
    c = llm_app(monkeypatch, llm_transport(["Ревью делает ", "наставник [delivery-code-review]."]))
    s = c.login("intern.anna")
    ev = s.say("как проходит код-ревью?")
    assert "наставник [delivery-code-review]" in text_of(ev)
    assert first(ev, "sources")["items"][0]["doc_id"] == "delivery-code-review"
    assert first(ev, "done")["provider"] == "openai_compat"
    # write scenarios stay deterministic with the LLM provider (ADR-007)
    assert first(s.say("хочу отгул в пятницу"), "action_proposal")["draft"]["args"]["start_date"] == "2026-11-13"


def test_S2_19_llm_failure_falls_back_to_stub(monkeypatch):
    c = llm_app(monkeypatch, llm_transport(status=503))
    ev = c.login("intern.anna").say("как проходит код-ревью?")
    assert "упрощённом режиме" in text_of(ev) and first(ev, "done")["provider"] == "stub_fallback"


def test_S2_20_llm_failure_without_fallback_is_retryable_error(monkeypatch):
    c = llm_app(monkeypatch, llm_transport(status=503), LLM_FALLBACK_TO_STUB="false")
    ev = c.login("intern.anna").say("как проходит код-ревью?")
    assert ev[-1].name == "error" and ev[-1].data["retryable"] is True


def test_S2_21_llm_tool_calling_only_read_tools_of_role(monkeypatch):
    seen_tools: list = []
    calls = [
        {
            "content": "",
            "tool_calls": [
                {"id": "1", "function": {"name": "directory__get_my_contacts", "arguments": "{}"}},
                {"id": "2", "function": {"name": "approvals__list_pending", "arguments": "{}"}},
                {"id": "3", "function": {"name": "kb__search", "arguments": "{not json"}},
            ],
        },
        {"content": "готово"},
    ]

    base = llm_transport(["Ответ"], calls)

    def spy(req: httpx.Request) -> httpx.Response:
        body = json.loads(req.content)
        if body.get("tools"):
            seen_tools.append([t["function"]["name"] for t in body["tools"]])
        return base.handle_request(req)

    c = llm_app(monkeypatch, httpx.MockTransport(spy), LLM_TOOL_CALLING="true")
    ev = c.login("intern.anna").say("как проходит код-ревью?")
    assert "Ответ" in text_of(ev)
    offered = seen_tools[0]
    assert "kb__search" in offered and "approvals__list_pending" not in offered
    assert not any(n.endswith("create_request") or n.endswith("request_access") for n in offered)
    assert "approvals.list_pending" not in first(ev, "done")["tool_calls"]  # denied by the gateway, never executed


def test_LLM_04_make_writer_and_stub():
    cfg = load_agent_config(env={})
    assert isinstance(make_writer(cfg, ""), StubWriter)
    cfg2 = load_agent_config(env={"LLM_PROVIDER": "openai_compat", "LLM_BASE_URL": "http://x/v1", "LLM_MODEL": "m"})
    assert isinstance(make_writer(cfg2, "k"), LLMWriter)


def test_LLM_05_embeddings_client():
    def handler(req):  # noqa: ANN001, ANN202
        body = json.loads(req.content)
        return httpx.Response(
            200, json={"data": [{"index": i, "embedding": [float(i)]} for i in range(len(body["input"]))]}
        )

    e = EmbeddingsClient("http://llm/v1", "k", "bge-m3", client=httpx.Client(transport=httpx.MockTransport(handler)))
    assert e(["a", "b"]) == [[0.0], [1.0]]


def test_MCP_15_mcp_base_url_routes_every_server(monkeypatch):
    monkeypatch.setenv("MCP_BASE_URL", "http://mcp-mocks:8100/mcp")
    settings = make_settings(mcp_base_url="http://mcp-mocks:8100/mcp")
    agent = build_agent(settings, load_agent_config())
    assert {k: v.url for k, v in agent.backends.items()}["absence"] == "http://mcp-mocks:8100/mcp/absence"
    assert load_agent_config().sanitized("intern")["mcp_servers"]["kb"] == "http"


def test_LLM_06_false_not_found_over_a_covering_document_is_replaced():
    """A small local model (A-Vibe Q4) sometimes says «не нашёл» although the document answers: the answer then comes
    from the document; a «not found» over a weak hit and a normal answer are streamed as they are."""
    from agent.auth import User
    from agent.llm.writer import LLMWriter, StubWriter

    cfg = load_agent_config(env={"LLM_PROVIDER": "openai_compat", "LLM_BASE_URL": "http://llm/v1", "LLM_MODEL": "m"})
    user = User(user_id="intern.anna", role="intern", team_id="delivery", token="t")
    hit = {
        "doc_id": "common-laptop",
        "title": "Ноутбук и техника",
        "snippet": "Забери ноутбук на стойке поддержки.",
        "score": 3,
    }

    def writer(chunks):  # noqa: ANN001, ANN202
        client = OpenAICompatClient("http://llm/v1", "", "m", client=httpx.Client(transport=llm_transport(chunks)))
        return LLMWriter(cfg, client, StubWriter())

    meta: dict = {}
    out = "".join(
        writer(["В базе знаний команды я этого не нашёл."]).answer(
            "где ноут?", [{**hit, "coverage": 1.0}], user, meta=meta
        )
    )
    assert "стойке поддержки" in out and "не нашёл" not in out and meta["llm_not_found_override"]
    weak = "".join(writer(["Я этого не нашёл."]).answer("где ноут?", [{**hit, "coverage": 0.3}], user))
    assert weak == "Я этого не нашёл."
    ok = "".join(writer(["Забери ноутбук", " на стойке."]).answer("где ноут?", [{**hit, "coverage": 1.0}], user))
    assert ok == "Забери ноутбук на стойке."
