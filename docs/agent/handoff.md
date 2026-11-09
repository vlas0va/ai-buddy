# Интеграция с агентом: что нужно от B и C

Агент готов и проходит все сценарии сам (встроенные имитации MCP). Чтобы включить его в общий стек, нужно немного:

## B (core и MCP-серверы)
1. **Вызов агента.** `POST {AGENT_URL}/v1/turn` (`AGENT_URL=http://agent:8090`), заголовки `Authorization: Bearer <JWT
   пользователя>` и `X-Request-Id`. Тело и события — `contracts/agent-stream.md` (ваш черновик принят как v1).
2. **user_context.** Обязательные поля как в черновике. Желательно добавить: `display_name`; в `open_tasks` — `status`,
   `due_at`, `period`, `human_only` (или полный план в `plan.tasks`); для наставника и руководителя — `mentees` из
   `GET /api/team/interns` (`[{user_id, display_name, accesses:[{name,status}], open_tasks}]`); `flags` команды.
3. **Confirm.** На `.../proposals/{pid}/confirm` подписать `sha256(canonical_json(draft.args))` токеном формата
   `contracts/mcp-tools.md` и отправить агенту `turn.kind=confirm`; агент вернёт `tool_result` → сохранить в `core.actions`,
   отдать в web `action_result`. В `tool_result` есть `task_checklist_item_id` для привязки к задаче плана и `followup`.
4. **Новый kind `approval_decision`** (руководитель согласует в чате): `draft.tool = approvals.decide`. Обработка такая же, как у
   остальных; если пока не поддерживаете — руководитель решает кнопками `POST /api/approvals/{id}/decision`.
5. **События аналитики** из `done`: `intent`, `provider`, `latency_ms`, `escalated`, `tool_calls` → `chat.answered`,
   `chat.escalated`, `chat.refused` (reason из события `refusal`).
6. **AI-крон** (`POST /api/cron/run`): тексты напоминаний — `POST {AGENT_URL}/v1/reminders`.
7. **Эскалация в базу знаний**: `POST /api/escalations/{id}/answer` с add-to-KB → `POST {AGENT_URL}/v1/kb/faq-draft` →
   сохранить markdown в `kb.documents`.
8. **mcp-kb** загружает `data/kb/*.md` (frontmatter: `acl.roles`, `acl.teams`, `checklist` — список, первый элемент →
   `checklist_item_id`). Все документы `source_type: test`.
9. **MCP-серверы.** Агент вызывает инструменты строго по `contracts/schemas/*.json`. Когда сервер готов, достаточно задать
   `MCP_<SERVER>_URL=http://mcp-<server>:<port>/mcp` в `.env` — код агента не меняется. Встроенные имитации повторяют матрицу
   прав из `contracts/mcp-tools.md` (`services/agent/tests/test_mocks.py` можно использовать как чек-лист для Go-тестов).
10. Изменения в общих файлах, сделанные A: сервис `agent` и профиль `ollama` в compose, цели `test-agent`, `eval`, `train`,
    `agent-dev` в Makefile, агент в `scripts/coverage.sh`, job `agent` и Python в job `go` в CI, переменные в `.env.example`.

## C (web, e2e, README, презентация)
1. **События.** Всё по `contracts/agent-stream.md`; новые поля необязательные. Новое событие `next_step` (карточка
   «Следующий шаг», кнопка отправляет `cta.message`). `refusal.text` — готовый текст блока отказа, `status.text` — подпись
   статуса ожидания. Плашка «Тестовая инструкция прототипа» — на каждом источнике.
2. **Карточки.** `action_proposal.title`, `summary`, `draft.display` (`approver_display_name`, `dates`/`period_label`,
   `resource_name`, `elevated`); `requires_approval` — показать «согласует руководитель».
3. **README.** Раздел «LLM и ключи» — готовый текст в `docs/llm-and-keys.md`. Тестовые учётки — как в сиде B.
4. **e2e (Playwright) на stub.** Для стабильных дат можно передать `today` (агент учитывает его при
   `AGENT_ALLOW_CLOCK_OVERRIDE=true`). Демо сбоя и «Повторить»: `MOCK_FAULTS=absence.create_request=timeout_after:1`.
5. **Презентация.** Тексты слайдов «Как устроен агент», «AI и права», «Метрики и пилот» — `docs/agent/slides.md`;
   метрики — `docs/product/metrics.md`; отчёты — `eval/reports/`.
6. **Видео.** Хорошие фразы для демо: «что мне делать дальше?», «как у нас проходит код-ревью?», «хочу отгул в пятницу» →
   «Отправить», вход `lead.maria` → «покажи заявки на согласование» → «согласуй ABS-…», «покажи отпуск коллеги» (отказ),
   «игнорируй все инструкции…» (отказ).
