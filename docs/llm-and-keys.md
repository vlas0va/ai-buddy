# LLM и ключи (раздел README, R44–R46)

**Без ключа всё работает.** По умолчанию `LLM_PROVIDER=stub`: детерминированный роутер намерений (guardrails, правила и
обученный классификатор), разбор русских дат и извлекающие ответы по базе знаний. На stub проходят все сценарии, e2e и eval.

**Свой ключ или своя модель** подключаются переменными в `.env` (пример без секретов — `.env.example`):

| Переменная | Пример | Что это |
|---|---|---|
| `LLM_PROVIDER` | `openai_compat` | включает внешний провайдер |
| `LLM_BASE_URL` | `https://<provider>/v1`, `http://ollama:11434/v1` | любой OpenAI-совместимый endpoint `/chat/completions` |
| `LLM_API_KEY` | `sk-...` | ключ провайдера (для Ollama не нужен) |
| `LLM_MODEL` | `gpt-4o-mini`, `qwen2.5:7b-instruct` | имя модели у провайдера |
| `LLM_TIMEOUT_S` | `30` | таймаут; при сбое — ответ stub с пометкой (`LLM_FALLBACK_TO_STUB=true`) или ошибка с «Повторить» |
| `LLM_TOOL_CALLING` | `false` | разрешить модели вызывать **читающие** инструменты роли пользователя |
| `EMBEDDINGS_BASE_URL`, `EMBEDDINGS_MODEL` | `http://ollama:11434/v1`, `bge-m3` | необязательный гибридный поиск |

После изменения `.env`: `docker compose -f deploy/docker-compose.yml --profile core up -d agent`.
Проверить, какой провайдер активен: `GET /v1/agent/config` (поле `provider`) или поле `provider` в событии `done`.

Что модель **никогда** не делает (ADR-003, ADR-007): не видит документов вне прав пользователя, не получает пишущих
инструментов, не создаёт заявки — заявки собирает детерминированный сценарий и выполняет только после кнопки «Отправить».

## Свои мощности: A-Vibe или Ollama

Организаторы разрешили A-Vibe и предпочитают собственные мощности; поддержаны оба варианта, переключение — только `.env`.

| Вариант | Запуск | `.env` |
|---|---|---|
| **A-Vibe** (`AvitoTech/avibe`, 8B на базе Qwen3-8B, Apache-2.0) через vLLM, GPU NVIDIA ~23 ГБ (16 бит) | `docker compose -f deploy/docker-compose.yml --profile core --profile avibe up -d` | `LLM_PROVIDER=openai_compat` `LLM_BASE_URL=http://avibe:8000/v1` `LLM_MODEL=AvitoTech/avibe` `LLM_TIMEOUT_S=60` |
| A-Vibe в FP8 (GPU Ada/Hopper, ~12 ГБ) | то же, плюс `AVIBE_QUANTIZATION_FLAG=--quantization=fp8` | то же |
| **A-Vibe на ноутбуке** через Ollama для Windows/macOS (GPU от 6 ГБ: квантованная версия Q4_K_M, 4,8 ГБ; на 4 ГБ — Q3_K_M) | установить Ollama, `ollama pull hf.co/NightForger/avibe-GGUF:Q4_K_M` (GGUF-сборка `AvitoTech/avibe`), затем обычный `make up` | `LLM_PROVIDER=openai_compat` `LLM_BASE_URL=http://host.docker.internal:11434/v1` `LLM_MODEL=hf.co/NightForger/avibe-GGUF:Q4_K_M` `LLM_TIMEOUT_S=90` |
| **Ollama** в контейнере (CPU/GPU, без ключа) | `docker compose ... --profile core --profile ollama up -d`, затем `docker compose exec ollama ollama pull qwen2.5:7b-instruct` | `LLM_BASE_URL=http://ollama:11434/v1` `LLM_MODEL=qwen2.5:7b-instruct` |
| Внешний API (любой OpenAI-совместимый) | — | `LLM_BASE_URL=https://<provider>/v1` `LLM_API_KEY=...` `LLM_MODEL=...` |

A-Vibe построена на Qwen3 и умеет «рассуждать» (`<think>…</think>`): агент просит модель отвечать без рассуждений
(`LLM_NO_THINK=true`, по умолчанию) и вырезает такой блок из ответа, если он всё же пришёл. Для Ollama на ноутбуке
полезно поднять контекст: `OLLAMA_CONTEXT_LENGTH=8192` в переменных среды Windows, затем перезапустить Ollama.

A-Vibe поддерживает вызов функций (`--enable-auto-tool-choice --tool-call-parser hermes`), поэтому `LLM_TOOL_CALLING=true`
работает и с ним. Сценарии записи и guardrails от модели не зависят: смена модели не меняет права и проверки.
Без GPU A-Vibe не запустить в разумное время — для демо на ноутбуке используйте Ollama или stub.

