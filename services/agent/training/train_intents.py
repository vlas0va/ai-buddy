"""Train the intent classifier: python -m training.train_intents

Writes agent/models/intent_nb.json and eval/reports/intents.md with:
  * 5-fold cross-validation on eval/intents/train.yaml (logistic regression vs naive Bayes baseline),
  * accuracy on the held-out eval/intents/test.yaml for the classifier alone and for the full router
    (guardrails + rules + classifier), which is what runs in production.
Runs in seconds on any laptop. Extend the dataset (or augment it with training/augment_with_llm.py) and retrain.
"""

from __future__ import annotations

import argparse
import random
from collections import Counter, defaultdict
from pathlib import Path

import yaml

from agent.config import REPO_ROOT, SERVICE_ROOT
from agent.nlu.classifier import IntentModel, NaiveBayesBaseline
from agent.nlu.router import IntentRouter


def load_samples(path: Path) -> list[tuple[str, str]]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    return [(text, label) for label, texts in data.items() for text in texts]


def cross_validate(samples: list[tuple[str, str]], folds: int = 5, seed: int = 1) -> tuple[float, float]:
    idx = list(range(len(samples)))
    random.Random(seed).shuffle(idx)
    parts = [idx[i::folds] for i in range(folds)]
    lr_ok = nb_ok = 0
    for part in parts:
        held = set(part)
        train = [samples[i] for i in idx if i not in held]
        lr, nb = IntentModel.train(train), NaiveBayesBaseline(train)
        for i in part:
            text, label = samples[i]
            lr_ok += lr.predict(text).intent == label
            nb_ok += nb.predict(text) == label
    return lr_ok / len(samples), nb_ok / len(samples)


def evaluate(model: IntentModel, test: list[tuple[str, str]]) -> tuple[float, float, dict[str, Counter[str]]]:
    router = IntentRouter(model, 0.3)
    confusion: dict[str, Counter[str]] = defaultdict(Counter)
    clf_ok = router_ok = 0
    for text, label in test:
        clf_ok += model.predict(text).intent == label
        got = router.route(text).intent
        confusion[label][got] += 1
        router_ok += got == label
    return clf_ok / len(test), router_ok / len(test), confusion


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--train", default=str(REPO_ROOT / "eval" / "intents" / "train.yaml"))
    ap.add_argument("--test", default=str(REPO_ROOT / "eval" / "intents" / "test.yaml"))
    ap.add_argument("--out", default=str(SERVICE_ROOT / "agent" / "models" / "intent_model.json"))
    ap.add_argument("--report", default=str(REPO_ROOT / "eval" / "reports" / "intents.md"))
    ap.add_argument("--skip-cv", action="store_true")
    args = ap.parse_args(argv)

    samples = load_samples(Path(args.train))
    test = load_samples(Path(args.test))
    model = IntentModel.train(samples)
    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    model.save(Path(args.out))
    clf_acc, router_acc, confusion = evaluate(model, test)
    labels = sorted({lbl for _, lbl in samples})
    lines = [
        "# Intent classifier report",
        "",
        f"Train: {len(samples)} phrases, {len(labels)} intents · held-out test: {len(test)} phrases · "
        f"model: softmax logistic regression, {model.meta['vocab']} features (stems + char 3–4-grams)",
        "",
    ]
    if not args.skip_cv:
        lr_cv, nb_cv = cross_validate(samples)
        lines += [
            "| metric | value |",
            "|---|---|",
            f"| 5-fold CV, logistic regression | {lr_cv:.1%} |",
            f"| 5-fold CV, naive Bayes baseline | {nb_cv:.1%} |",
        ]
        print(f"cv: lr={lr_cv:.3f} nb={nb_cv:.3f}")
    else:
        lines += ["| metric | value |", "|---|---|"]
    lines += [
        f"| held-out test, classifier only | {clf_acc:.1%} |",
        f"| held-out test, full router (guardrails + rules + classifier) | **{router_acc:.1%}** |",
        "",
        "## Router confusion on the held-out set",
        "",
        "| intent | n | correct | routed instead |",
        "|---|---|---|---|",
    ]
    for lbl in sorted(confusion):
        row = confusion[lbl]
        wrong = [(k, v) for k, v in row.most_common() if k != lbl]
        lines.append(
            f"| {lbl} | {sum(row.values())} | {row[lbl]} | {', '.join(f'{k} ({v})' for k, v in wrong) or '—'} |"
        )
    lines += [
        "",
        "Write scenarios (S3, S4, approvals) are matched by high-precision rules before the classifier, and "
        "refusals by deterministic guardrails, so classifier mistakes mostly land in `kb_question` "
        "(an answer from the knowledge base or an escalation draft) — never in a write action.",
    ]
    Path(args.report).parent.mkdir(parents=True, exist_ok=True)
    Path(args.report).write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"test: classifier={clf_acc:.3f} router={router_acc:.3f}\nmodel → {args.out}\nreport → {args.report}")
    return 0


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
