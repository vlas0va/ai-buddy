"""Intent classifier: multinomial logistic regression (softmax) on stemmed words and char n-grams (ADR-008).

Pure Python, deterministic (fixed seed), trains in seconds (`python -m training.train_intents`); the model is a JSON file
in the repo. A naive Bayes baseline is kept for comparison in the training report.
"""

from __future__ import annotations

import json
import math
import random
from collections import Counter, defaultdict
from dataclasses import dataclass
from pathlib import Path

from ..text import char_ngrams, stems

CHAR_WEIGHT = 0.5


def features(text: str) -> dict[str, float]:
    st = stems(text)
    raw: dict[str, float] = {f"w:{s}": 1.0 for s in st}
    raw.update({f"c:{g}": CHAR_WEIGHT for g in char_ngrams(text, 3, 4)})
    norm = math.sqrt(sum(v * v for v in raw.values())) or 1.0
    return {k: v / norm for k, v in raw.items()}


@dataclass
class Prediction:
    intent: str
    confidence: float
    ranking: list[tuple[str, float]]


class IntentModel:
    def __init__(self, weights: dict[str, dict[str, float]], bias: dict[str, float], meta: dict | None = None) -> None:
        self.weights = weights
        self.bias = bias
        self.meta = meta or {}

    @property
    def labels(self) -> list[str]:
        return sorted(self.bias)

    @classmethod
    def train(
        cls, samples: list[tuple[str, str]], epochs: int = 40, lr: float = 0.8, l2: float = 1e-4, seed: int = 7
    ) -> IntentModel:
        labels = sorted({lbl for _, lbl in samples})
        w: dict[str, defaultdict[str, float]] = {lbl: defaultdict(float) for lbl in labels}
        b = {lbl: 0.0 for lbl in labels}
        data = [(features(t), lbl) for t, lbl in samples]
        rnd = random.Random(seed)
        for epoch in range(epochs):
            rnd.shuffle(data)
            eta = lr / (1 + epoch * 0.1)
            for x, y in data:
                probs = _softmax({lbl: b[lbl] + sum(w[lbl].get(k, 0.0) * v for k, v in x.items()) for lbl in labels})
                for lbl in labels:
                    g = probs[lbl] - (1.0 if lbl == y else 0.0)
                    if abs(g) < 1e-4:
                        continue
                    wl = w[lbl]
                    for k, v in x.items():
                        wl[k] -= eta * (g * v + l2 * wl[k])
                    b[lbl] -= eta * g
        pruned = {lbl: {k: round(v, 4) for k, v in w[lbl].items() if abs(v) >= 0.005} for lbl in labels}
        vocab = len({k for d in pruned.values() for k in d})
        return cls(
            pruned,
            {k: round(v, 4) for k, v in b.items()},
            {"samples": len(samples), "vocab": vocab, "epochs": epochs, "lr": lr, "l2": l2, "seed": seed},
        )

    def predict(self, text: str) -> Prediction:
        x = features(text)
        known = {k: v for k, v in x.items() if any(k in d for d in self.weights.values())}
        if not known:
            return Prediction("kb_question", 0.0, [])
        probs = _softmax(
            {
                lbl: self.bias[lbl] + sum(self.weights[lbl].get(k, 0.0) * v for k, v in known.items())
                for lbl in self.bias
            }
        )
        ranking = sorted(probs.items(), key=lambda kv: kv[1], reverse=True)
        return Prediction(ranking[0][0], round(ranking[0][1], 4), [(k, round(p, 4)) for k, p in ranking[:3]])

    def to_json(self) -> dict:
        return {"format": "lr-intent/v1", "meta": self.meta, "bias": self.bias, "weights": self.weights}

    def save(self, path: Path) -> None:
        Path(path).write_text(
            json.dumps(self.to_json(), ensure_ascii=False, sort_keys=True, separators=(",", ":")), encoding="utf-8"
        )

    @classmethod
    def load(cls, path: Path) -> IntentModel:
        data = json.loads(Path(path).read_text(encoding="utf-8"))
        if data.get("format") != "lr-intent/v1":
            raise ValueError("unsupported intent model format")
        return cls(data["weights"], data["bias"], data.get("meta"))


def _softmax(scores: dict[str, float]) -> dict[str, float]:
    mx = max(scores.values())
    exp = {k: math.exp(v - mx) for k, v in scores.items()}
    z = sum(exp.values())
    return {k: v / z for k, v in exp.items()}


class NaiveBayesBaseline:
    """Multinomial NB on the same features — baseline for the training report only."""

    def __init__(self, samples: list[tuple[str, str]], alpha: float = 0.1) -> None:
        by: dict[str, Counter[str]] = defaultdict(Counter)
        count: Counter[str] = Counter()
        vocab: set[str] = set()
        for t, lbl in samples:
            f = features(t)
            by[lbl].update(f)
            count[lbl] += 1
            vocab.update(f)
        n = sum(count.values())
        self.prior = {lbl: math.log(c / n) for lbl, c in count.items()}
        self.ll: dict[str, dict[str, float]] = {}
        self.unseen: dict[str, float] = {}
        for lbl, c in by.items():
            total = sum(c.values()) + alpha * (len(vocab) + 1)
            self.ll[lbl] = {k: math.log((v + alpha) / total) for k, v in c.items()}
            self.unseen[lbl] = math.log(alpha / total)

    def predict(self, text: str) -> str:
        f = features(text)
        return max(
            self.prior,
            key=lambda lbl: self.prior[lbl] + sum(v * self.ll[lbl].get(k, self.unseen[lbl]) for k, v in f.items()),
        )
