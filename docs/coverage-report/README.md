# Отчёт о покрытии тестами

Снимок на 14.11.2026, ветка `rita/avibe`. Покрытие строк кода, написанного командой (без зависимостей, сгенерированного кода и самих тестов). Порог 80% проверяется в CI; там же отчёты выкладываются артефактом `coverage`.

| Сервис | Покрытие строк | Отчёт |
|---|---|---|
| core (Go) | 87.5% | [go/core.html](go/core.html) |
| mcp-mocks (Go) | 87.1% | [go/mocks.html](go/mocks.html) |
| platform (Go, общие библиотеки) | 98.1% | [go/platform.html](go/platform.html) |
| agent (Python) | 96.0% (253 теста) | [agent/html/index.html](agent/html/index.html), [agent/cobertura.xml](agent/cobertura.xml) |
| web (TypeScript) | 93.4% (194 теста) | [web/index.html](web/index.html), [web/cobertura-coverage.xml](web/cobertura-coverage.xml) |

Как воспроизвести:

```bash
make coverage                        # Go и агент → coverage/<service>/ (поднимает одноразовый Postgres)
cd web && npx vitest run --coverage  # web → web/coverage/
```

HTML-файлы открываются в браузере напрямую.
