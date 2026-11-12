# Примеры конфигурации LLM

Строки из файла копируются в `.env` в корне репозитория (шаблон всех переменных — `.env.example`), затем:

```bash
docker compose -f deploy/docker-compose.yml --profile core up -d agent
```

| Файл | Что | Ключ |
|---|---|---|
| `avibe-bundled.env` | **по умолчанию**: A-Vibe в Ollama из compose (ничего задавать не нужно) | не нужен |
| `avibe-host-ollama.env` | A-Vibe в Ollama, установленной на самом компьютере (быстрее с GPU) | не нужен |
| `openai-compatible.env` | любой облачный OpenAI-совместимый API (ключ организаторов) | `LLM_API_KEY` |
| `stub.env` | без модели: детерминированный режим, как в CI и e2e | не нужен |

Проверить, какая модель отвечает: `curl localhost:8090/readyz` → `checks.provider`, `checks.llm_model`,
`checks.llm_state` (`ready` / `loading` / `slow` / `down`); в каждом ответе чата событие `done.provider`
(`openai_compat` — модель, `stub_fallback` — модель недоступна, ответил stub).
