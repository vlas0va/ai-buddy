"""Regenerate vectors.json: canonical JSON and SHA-256 computed by Python, checked byte-for-byte by Go tests.

Run from the repository root: python3 -I services/internal/platform/canonjson/testdata/gen_vectors.py
The agent (Python) must produce exactly these bytes: json.dumps(obj, sort_keys=True, separators=(",", ":"),
ensure_ascii=False).encode("utf-8"). Floats are excluded on purpose (see the package doc of canonjson).
"""

import hashlib
import json
import pathlib

INPUTS = [
    # S3 draft args from contracts/agent-stream.md
    '{"type":"day_off","start_date":"2026-11-14","end_date":"2026-11-14","comment":""}',
    # S4 draft args
    '{"resource_id":"space-delivery","reason":""}',
    # approvals.decide args issued by core
    '{"approval_id":"0b9a3f6e-2c1d-4e5f-8a7b-9c0d1e2f3a4b","decision":"approve","comment":"Ок, хорошего отдыха"}',
    # whitespace, nesting, empty containers, array order
    ' { "b" : 1 , "a" : [ 3 , 2 , { "z" : null , "y" : true , "x" : false } ] , "c" : { } , "d" : [ ] } ',
    # key order by code point: ASCII, Latin-1, Cyrillic, BMP private use, astral (UTF-16 order would differ)
    '{"я":1,"а":2,"z":3,"Z":4,"\\u00e9":5,"\\ue000":6,"\\ud83d\\ude00":7,"":8,"aa":9,"a":10}',
    # string escapes
    (
        '{"s":"quote\\" backslash\\\\ slash/ nl\\n cr\\r tab\\t bs\\b ff\\f nul\\u0000 us\\u001f del\\u007f'
        ' ls\\u2028 ps\\u2029 html<>& emoji😀 кириллица"}'
    ),
    # escaped input is decoded, then re-encoded canonically
    '{"k":"\\u0041\\/\\u00e9\\u0430"}',
    # integers of any size, -0
    "[0,-0,-1,10,9007199254740993,-123456789012345678901234567890]",
    # duplicate keys keep the last value
    '{"a":1,"a":2}',
    # top-level scalars
    '"строка"',
    "true",
    "null",
    "42",
    # deep nesting
    '{"outer":{"inner":{"list":[{"b":[],"a":{}},[[]],"x"]}}}',
]


def canonical(obj):
    return json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode("utf-8")


def main():
    vectors = []
    for text in INPUTS:
        out = canonical(json.loads(text))
        vectors.append(
            {
                "input": text,
                "canonical": out.decode("utf-8"),
                "sha256": hashlib.sha256(out).hexdigest(),
            }
        )
    path = pathlib.Path(__file__).with_name("vectors.json")
    path.write_text(
        json.dumps(vectors, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
