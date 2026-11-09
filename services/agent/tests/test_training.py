"""Intent model training and routing quality (ADR-008)."""

from __future__ import annotations

from pathlib import Path

from agent.nlu.classifier import IntentModel, NaiveBayesBaseline
from agent.nlu.router import IntentRouter
from training import train_intents

ROOT = Path(__file__).resolve().parents[3]


def test_NLU_01_train_save_load_predict(tmp_path):
    samples = [
        ("хочу отгул", "absence"),
        ("отгул в пятницу", "absence"),
        ("какие доступы нужны", "access"),
        ("запроси доступ в jira", "access"),
    ]
    m = IntentModel.train(samples, epochs=10)
    m.save(tmp_path / "m.json")
    loaded = IntentModel.load(tmp_path / "m.json")
    assert loaded.predict("отгул завтра").intent == "absence" and loaded.labels == ["absence", "access"]
    assert loaded.predict("zzzz qqqq").intent in ("kb_question", "absence", "access")
    assert NaiveBayesBaseline(samples).predict("доступ в jira") == "access"
    (tmp_path / "bad.json").write_text('{"format": "x"}')
    import pytest

    with pytest.raises(ValueError):
        IntentModel.load(tmp_path / "bad.json")


def test_NLU_02_training_script_and_router_quality(tmp_path):
    out, report = tmp_path / "m.json", tmp_path / "r.md"
    assert train_intents.main(["--out", str(out), "--report", str(report), "--skip-cv"]) == 0
    text = report.read_text()
    assert "held-out test, full router" in text
    model = IntentModel.load(out)
    test = train_intents.load_samples(ROOT / "eval" / "intents" / "test.yaml")
    router = IntentRouter(model, 0.3)
    acc = sum(router.route(t).intent == lbl for t, lbl in test) / len(test)
    assert acc >= 0.85


def test_NLU_03_cross_validation_runs():
    samples = train_intents.load_samples(ROOT / "eval" / "intents" / "train.yaml")[::4]
    lr, nb = train_intents.cross_validate(samples, folds=2)
    assert 0 <= nb <= 1 and 0 <= lr <= 1


def test_NLU_04_router_without_model_falls_back():
    r = IntentRouter(None)
    assert r.route("").intent == "help"
    assert r.route("как устроен мир").intent == "kb_question"


def test_NLU_05_augmentation_with_llm_drops_test_collisions(tmp_path):
    import json as _json

    import httpx

    from agent.llm.openai_compat import OpenAICompatClient
    from training import augment_with_llm

    answers = iter([_json.dumps(["мне бы отгул в среду", "что по плану"]), "- новая фраза\n- ещё одна"])

    def handler(req):  # noqa: ANN001, ANN202
        return httpx.Response(200, json={"choices": [{"message": {"content": next(answers, "[]")}}]})

    client = OpenAICompatClient("http://llm/v1", "", "m", client=httpx.Client(transport=httpx.MockTransport(handler)))
    out = tmp_path / "aug.yaml"
    assert augment_with_llm.main(["--per-intent", "2", "--out", str(out)], client=client) == 0
    text = out.read_text(encoding="utf-8")
    assert "мне бы отгул в среду" in text and "что по плану" not in text  # held-out phrase dropped
    assert "новая фраза" in text
