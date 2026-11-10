# ADR-005. SDD через OpenSpec

Статус: принято · Владелец: A · Требования: R37, R60, R75

## Решение
AI-функции разрабатываются spec-first в OpenSpec: `openspec/changes/<change>/` (proposal, design, tasks, дельта спеки) →
реализация → тесты, в имени которых ID сценария спеки (`test_S3_05_...`) → `openspec archive` переносит дельту в `openspec/specs/`.
Архив изменений — доказательство, что разработка шла по спекам. `openspec validate --all` — в CI.

## Почему OpenSpec, а не spec-kit
Легче: нет жёсткой цепочки constitution → specify → plan → tasks, а на хакатоне частые маленькие изменения важнее полного цикла.
Формат требований (SHALL + WHEN/THEN) напрямую переводится в тесты и eval-кейсы.

## Трассировка
Спека → сценарий `S3-05` → тест `test_S3_05_*` → eval-кейс `s3-*` → строка матрицы требований R66. Скрипт
`services/agent/tools/spec_trace.py` проверяет, что у каждого сценария спеки есть тест.
