# AI-buddy · сервис агента (зона A)

Python 3.12 + Flask, ответ потоком SSE. Работает **без ключа LLM** (`LLM_PROVIDER=stub` по умолчанию) и с любым
OpenAI-совместимым API. Все корпоративные действия — через MCP-инструменты по ролям; запись — только после подтверждения.

| Что | Где |
|---|---|
| Контракт с core и web | [`contracts/agent-stream.md`](../../contracts/agent-stream.md) (v1, подтверждён A) |
| Конфиг агента «промпт + модель + MCP + инструменты по ролям» (R53) | [`agent.yaml`](agent.yaml) |
| Системный промпт (версионируется) | [`prompts/system.v1.md`](prompts/system.v1.md), [`prompts/answer.v1.md`](prompts/answer.v1.md) |
| SDD-спеки AI-функций и архив изменений (R37, R60, R75) | [`openspec/specs`](../../openspec/specs), [`openspec/changes/archive`](../../openspec/changes/archive) |
| ADR по агенту | [`docs/adr`](../../docs/adr) — 001, 003–008 |
| Синтетическая база знаний (37 документов, 2 команды) | [`data/kb`](../../data/kb) |
| Eval-набор (92 кейса) и отчёты | [`eval/`](../../eval), [`eval/reports/latest.md`](../../eval/reports/latest.md) |

## Быстрый старт

```bash
cd services/agent
python -m pip install -r requirements-dev.txt
make test        # 174 теста, ~10 с, без сети и ключей
make coverage    # покрытие строк ~97% (порог 80%), HTML + Cobertura в coverage/agent
make eval        # 92 кейса, guard-метрики (отказы, инъекции, права) должны быть 100%
make run         # агент на :8090 + консоль разработчика http://localhost:8090/dev
```
В общем стеке: `make up` из корня поднимает агента вместе с core, MCP-серверами, Postgres и Redis (`deploy/docker-compose.yml`).

### Консоль разработчика `/dev`
Включается `AGENT_DEV_MODE=true` (в compose выключена). Имитирует то, что делают core и web: вход тестовыми учётками
(`intern.anna`, `intern.ivan`, `mentor.oleg`, `lead.maria`, `lead.dmitry`, `hrbp.elena`, `alumni.petr`), план из чек-листа,
выпуск confirmation-токена на «Отправить». Нужна, чтобы проверить и показать агента до готовности core и web.

## Как устроен ход диалога

```
POST /v1/turn {turn.kind: message}
  → JWT → User (роль и команда только из токена; user_context сверяется с claims)
  → guardrails (чужие данные, ПДн и финансы, найм/увольнение, инъекции, «замени наставника»)   ← рубеж 0
  → правила высокой точности → классификатор намерений (логистическая регрессия) → порог
  → сценарий:
       S3 отгул / S4 доступы / согласования  — конечный автомат → action_proposal (запись НЕ выполняется)
       S2 вопрос                               — kb.search (ACL) → повторная проверка ACL → ответ stub или LLM + sources
       S1 план, сводка, контакты, эскалация    — детерминированно по данным инструментов
       Наставничество (CO-xx)                  — «учись, а не делегируй», подготовка к код-ревью, разбор лога
                                                 с маскированием секретов, квиз по инструкции, бенефиты без денег
  → SSE: status → token… → sources / action_proposal / escalation_draft / next_step / refusal → done | error

POST /v1/turn {turn.kind: confirm, proposal_id, draft, confirmation_token}
  → проверка токена (подпись, exp, sub, key, args_hash) + реестр proposal → MCP write с idempotency_key = proposal_id
  → SSE: status → tool_result (core сохраняет action и шлёт action_result) → done
```

## Конфигурация (переменные окружения)

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `LLM_PROVIDER` | `stub` | `stub` или `openai_compat` |
| `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL` | пусто | любой OpenAI-совместимый endpoint (облако, vLLM, Ollama) |
| `LLM_TIMEOUT_S`, `LLM_TEMPERATURE` | `30`, `0.2` | таймаут вызова и температура |
| `LLM_TOOL_CALLING` | `false` | LLM может вызывать **читающие** инструменты своей роли |
| `LLM_FALLBACK_TO_STUB` | `true` | при сбое LLM ответ строит stub, пользователь видит пометку |
| `EMBEDDINGS_BASE_URL`, `EMBEDDINGS_MODEL` | пусто | гибридный поиск BM25 + векторы (например, `bge-m3` в Ollama) |
| `JWT_SECRET`, `CONFIRMATION_HMAC_SECRET` | dev-значения | те же, что у core и MCP-серверов |
| `MCP_BASE_URL` | пусто | `http://mcp-mocks:8100/mcp` — все серверы B по адресу `<base>/<server>` |
| `MCP_<SERVER>_URL` | пусто | адрес одного сервера (важнее `MCP_BASE_URL`); пусто и нет базы → встроенная имитация |
| `REDIS_URL` + `AGENT_USE_REDIS=true` | выкл. | состояние диалогов и реестр proposal в Redis |
| `AGENT_SERVICE_TOKEN` | пусто | общий секрет core → агент (`X-Service-Token`) |
| `AGENT_DEV_MODE`, `AGENT_ALLOW_CLOCK_OVERRIDE` | `false` | консоль `/dev`; поле `today` в запросе для демо и e2e |
| `MOCK_FAULTS` | пусто | имитация сбоев для демо R79: `absence.create_request=timeout_after:1` |

### Своя модель: Ollama на ноутбуке или A-Vibe на GPU
```bash
docker compose -f deploy/docker-compose.yml --profile core --profile ollama up -d
docker compose -f deploy/docker-compose.yml exec ollama ollama pull qwen2.5:7b-instruct
# .env
LLM_PROVIDER=openai_compat
LLM_BASE_URL=http://ollama:11434/v1
LLM_MODEL=qwen2.5:7b-instruct
```
Своя модель A-Vibe (`AvitoTech/avibe`) через vLLM: профиль `avibe` в compose, `LLM_BASE_URL=http://avibe:8000/v1`,
`LLM_MODEL=AvitoTech/avibe` — подробности и требования к GPU в `docs/llm-and-keys.md`.

Сценарии записи (S3, S4, согласования) от модели не зависят (ADR-007): LLM отвечает на вопросы по базе знаний и
формулирует текст, а заявки собирает детерминированный автомат.

## Обучение классификатора намерений
```bash
make train                              # секунды; пишет agent/models/intent_model.json и eval/reports/intents.md
python training/augment_with_llm.py     # опционально: парафразы от локальной LLM → eval/intents/augmented.yaml
```
Отчёт: 5-fold CV (логистическая регрессия против наивного Байеса) и точность на отложенном наборе для классификатора и
для всего роутера (сейчас 94%). Ошибки классификатора безопасны: пишущие сценарии запускают правила, а неуверенные
предсказания уходят в ответ по базе знаний.

## Тесты и трассировка
- Имена тестов содержат ID сценариев спек: `test_S3_05_confirmed_request_created_shown_saved`.
- `python tools/spec_trace.py --report ../../eval/reports/spec-trace.md` — у каждого из 140 сценариев есть тест.
- Матрица прав имитаций (`tests/test_mocks.py`) повторяет требования `contracts/mcp-tools.md` к Go-серверам B.
- MCP-клиент и LLM-провайдер проверяются через `httpx.MockTransport`: CI не нужны ни сеть, ни ключ.

## Наставничество вместо исполнения (ответы организаторов 10.11)

| Сообщение стажёра | Что делает buddy | Сценарий |
|---|---|---|
| «напиши за меня код задачи» | не делает: план из шагов, правила работы с нейросетями, когда звать наставника | CO-01 |
| «кого позвать на код-ревью?» | чек-лист перед ревью, правила и ревьюеры команды, черновик наставнику | CO-02, CO-03 |
| вставленный лог ошибки | главная строка, вероятная причина по базе знаний, секреты скрыты, черновик с выдержкой | CO-04 … CO-06 |
| «проверь меня по защите данных» | квиз с пропусками по тестовой инструкции, разбор ответов, подсказка отметить задачу плана | CO-07 … CO-11 |
| «подключи компенсацию спорта» | отказ от операции + справка с тестовыми цифрами и порядком действий | CO-12 |
| «как создать ветку?», «где получить ноутбук?» | ответы по `eng-*` и оргдокументам; раздел офиса — по `user_context.location` | CO-13, CO-14 |
