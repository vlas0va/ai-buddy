# Публичный стенд: VPS + HTTPS + автодеплой

Стенд — тот же стек, что `make up`, на арендованном сервере. Снаружи только HTTPS на адресе вида
`https://1-2-3-4.sslip.io` (sslip.io превращает IP в домен бесплатно, сертификат Let's Encrypt Caddy получает сам).
Модель — stub: отвечает сразу и проходит все сценарии, ключ не нужен.

После настройки каждый зелёный CI на `main` сам обновляет стенд (`.github/workflows/deploy.yml`).

## Что входит

| Файл | Что делает |
|---|---|
| `deploy/docker-compose.prod.yml` | Поверх основного compose: Caddy на 80/443, веб и API только через него, секреты из `.env`, stub, без Ollama |
| `deploy/Caddyfile` | `https://<DOMAIN>/api/*` → ядро (поток чата SSE без буферизации), остальное → веб |
| `deploy/server/bootstrap.sh` | Один раз на свежем сервере: Docker (+ зеркало Docker Hub Timeweb), файрвол, своп, пользователь `deploy`, клон, `.env` со случайными секретами, первый запуск |
| `deploy/server/deploy.sh` | Обновление: `git reset --hard origin/main`, `docker compose up -d --build --wait`, проверка входа по HTTPS |
| `.github/workflows/deploy.yml` | После зелёного CI на `main` заходит на сервер по SSH и запускает `deploy.sh` |

## 1. Сервер на Timeweb Cloud

1. timeweb.cloud → «Облачные серверы» → «Создать».
2. ОС: **Ubuntu 24.04**. Конфигурация: **2 vCPU, 4 ГБ RAM, 40+ ГБ NVMe** (для stub хватает; сборка образов идёт на сервере).
3. Регион любой. Нужен **публичный IPv4**.
4. В «SSH-ключи» добавьте свой публичный ключ (`ssh-keygen -t ed25519`, файл `~/.ssh/id_ed25519.pub`) — или войдите по паролю из письма.
5. Запомните IP, например `185.12.34.56`. Адрес стенда будет `https://185-12-34-56.sslip.io`.

## 2. Ключ для автодеплоя

На своём компьютере (PowerShell или Git Bash):

```bash
ssh-keygen -t ed25519 -f aibuddy_deploy -N "" -C "github-actions"
```

Получатся `aibuddy_deploy` (приватный — в GitHub) и `aibuddy_deploy.pub` (публичный — на сервер). В git их не кладите.

## 3. Первый запуск на сервере

```bash
ssh root@185.12.34.56
export DEPLOY_PUBKEY="<содержимое aibuddy_deploy.pub одной строкой>"
curl -fsSL https://raw.githubusercontent.com/Eliz-Kory/AI-Buddy/main/deploy/server/bootstrap.sh | bash
```

Займёт 5–10 минут. В конце скрипт напишет `Стенд работает: https://185-12-34-56.sslip.io`.

Если репозиторий приватный: на сервере `ssh-keygen -t ed25519` под пользователем `deploy`, ключ
`/home/deploy/.ssh/id_ed25519.pub` добавьте в GitHub → Settings → Deploy keys, и запустите скрипт с
`REPO=git@github.com:Eliz-Kory/AI-Buddy.git`.

## 4. Секреты GitHub

GitHub → репозиторий → Settings → Secrets and variables → Actions → New repository secret:

| Секрет | Значение |
|---|---|
| `DEPLOY_HOST` | IP сервера, например `185.12.34.56` |
| `DEPLOY_SSH_KEY` | Содержимое файла `aibuddy_deploy` целиком (с строками `-----BEGIN…` и `-----END…`) |
| `DEPLOY_USER` | Не обязательно, по умолчанию `deploy` |

Проверка: Actions → deploy → Run workflow. Дальше стенд обновляется сам после каждого мержа в `main` с зелёным CI.
Пока секретов нет, workflow просто пропускает деплой.

## Обслуживание

```bash
ssh deploy@185.12.34.56
cd /opt/aibuddy
bash deploy/server/deploy.sh                       # обновить вручную
docker compose --env-file .env -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml --profile core ps
docker compose --env-file .env -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml --profile core logs -f core agent
```

Сбросить демо-данные (заявки, чаты) к исходному состоянию:

```bash
docker compose --env-file .env -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml --profile core down
docker volume rm aibuddy_pgdata
bash deploy/server/deploy.sh
```

Модель вместо stub: добавьте в `/opt/aibuddy/.env` строки облачного OpenAI-совместимого API (`LLM_PROVIDER=openai_compat`,
`LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`, см. `deploy/llm/openai-compatible.env`) и выполните `deploy.sh`.
A-Vibe на сервере без GPU отвечает около минуты — для стенда не рекомендуется.

## Если что-то не так

- **Сертификат не выдан** (Caddy в логах ругается на ACME): проверьте, что порты 80 и 443 открыты в панели Timeweb
  (раздел «Сеть» / файрвол) и что `DOMAIN` в `.env` совпадает с IP сервера.
- **`pull access denied` / `403` при сборке**: Docker Hub недоступен с российских IP. Скрипт настраивает зеркало
  `https://dockerhub.timeweb.cloud` в `/etc/docker/daemon.json`; если файл уже был, добавьте туда `registry-mirrors` вручную и
  `systemctl restart docker`.
- **Не хватает памяти при сборке**: скрипт добавляет своп 2 ГБ; на сервере с 2 ГБ RAM сборка веба может падать — берите 4 ГБ.
- **Вход работает, а чат «не отвечает»**: `logs agent` — агент на stub должен отвечать сразу; `.env` должен содержать
  `LLM_DEFAULT_PROVIDER=stub`.

Безопасность: наружу открыты только 22, 80 и 443; Postgres, Redis, агент и MCP-серверы доступны только внутри сети Docker;
секреты JWT и подтверждений — случайные, в `.env` на сервере (не в git). Все данные синтетические, пароли учётных записей —
`demo`, как в README.
