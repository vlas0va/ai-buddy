# Разработка на Windows

Сервисы работают в Linux-контейнерах, поэтому на Windows нужен Docker Desktop и bash.

1. Docker Desktop с бэкендом WSL 2. Проверка: `docker compose version`.
2. Git for Windows (в комплекте Git Bash). Все команды `make` и `scripts/*.sh` запускать из Git Bash или из WSL.
3. `make`: в Git Bash его нет по умолчанию — `winget install ezwinports.make` или `choco install make`; в WSL — `sudo apt install make`.
4. Go 1.24, Python 3.12 (`pip install pyyaml jsonschema` для `make seed` и `make contracts`), Node 20.19+ для OpenSpec.
5. Переводы строк: в репозитории `.gitattributes` с `eol=lf`. После его появления один раз выполнить
   `git add --renormalize . && git commit -m "Normalize line endings"`.
6. Без `make` (например, в PowerShell) стек поднимается так:
   `docker compose -f deploy/docker-compose.yml --profile core up -d --build`.
   Эту же команду README даёт экспертам как основной способ запуска.
7. Порт 8080 занят — задать в `.env` `CORE_HOST_PORT=8081`; порт внутри контейнера не меняется.
