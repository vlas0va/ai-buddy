"""Пересобрать vectors.json: канонический JSON и SHA-256 считает Python, Go-тесты проверяют байт в байт.

Запускать из корня репозитория: python3 -I services/internal/platform/canonjson/testdata/gen_vectors.py
Агент (Python) должен выдавать ровно эти байты: json.dumps(obj, sort_keys=True, separators=(",", ":"),
ensure_ascii=False).encode("utf-8"). Числа с плавающей точкой исключены намеренно (см. документацию пакета canonjson).
"""

import hashlib
import json
import pathlib

INPUTS = [
    # args черновика S3 из contracts/agent-stream.md
    '{"type":"day_off","start_date":"2026-11-14","end_date":"2026-11-14","comment":""}',
    # args черновика S4
    '{"resource_id":"space-delivery","reason":""}',
    # args approvals.decide, которые выпускает ядро
    '{"approval_id":"0b9a3f6e-2c1d-4e5f-8a7b-9c0d1e2f3a4b","decision":"approve","comment":"Ок, хорошего отдыха"}',
    # пробелы, вложенность, пустые контейнеры, порядок массивов
    ' { "b" : 1 , "a" : [ 3 , 2 , { "z" : null , "y" : true , "x" : false } ] , "c" : { } , "d" : [ ] } ',
    # порядок ключей по кодовым точкам: ASCII, Latin-1, кириллица, частная область BMP, астральные (порядок UTF-16 был
    # бы другим)
    '{"я":1,"а":2,"z":3,"Z":4,"\\u00e9":5,"\\ue000":6,"\\ud83d\\ude00":7,"":8,"aa":9,"a":10}',
    # экранирование строк
    (
        '{"s":"quote\\" backslash\\\\ slash/ nl\\n cr\\r tab\\t bs\\b ff\\f nul\\u0000 us\\u001f del\\u007f'
        ' ls\\u2028 ps\\u2029 html<>& emoji😀 кириллица"}'
    ),
    # экранированный вход декодируется, затем кодируется заново канонически
    '{"k":"\\u0041\\/\\u00e9\\u0430"}',
    # целые любого размера, -0
    "[0,-0,-1,10,9007199254740993,-123456789012345678901234567890]",
    # при дубликатах ключей остаётся последнее значение
    '{"a":1,"a":2}',
    # скаляры верхнего уровня
    '"строка"',
    "true",
    "null",
    "42",
    # глубокая вложенность
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
