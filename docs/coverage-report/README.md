# Отчёт о покрытии тестами

Снимок на 11.11.2026, ветка `rita/extras`. Покрытие строк кода, написанного командой (без зависимостей, сгенерированного кода и самих тестов). Порог 80% проверяется в CI; там же отчёты выкладываются артефактом `coverage`.

| Сервис | Покрытие строк | Отчёт |
|---|---|---|
| core (Go) | 89.3% | [go/core.html](go/core.html) |
| mcp-mocks (Go) | 88.5% | [go/mocks.html](go/mocks.html) |
| platform (Go, общие библиотеки) | 98.0% | [go/platform.html](go/platform.html) |
| agent (Python) | 97.1% | [agent/html/index.html](agent/html/index.html), [agent/cobertura.xml](agent/cobertura.xml) |
| web (TypeScript) | 96.5% (159 тестов) | [web/index.html](web/index.html), [web/cobertura-coverage.xml](web/cobertura-coverage.xml) |

Как воспроизвести:

```bash
make coverage                        # Go и агент → coverage/<service>/ (поднимает одноразовый Postgres)
cd web && npx vitest run --coverage  # web → web/coverage/
```

HTML-файлы открываются в браузере напрямую.
