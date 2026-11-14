#!/usr/bin/env bash
# Обновление стенда до последнего коммита ветки и перезапуск. Запускает GitHub Actions (deploy.yml) по SSH
# или человек на сервере: cd /opt/aibuddy && bash deploy/server/deploy.sh
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/aibuddy}"
BRANCH="${BRANCH:-main}"
cd "$APP_DIR"

echo "==> Код: origin/$BRANCH"
git fetch --quiet origin "$BRANCH"
git reset --quiet --hard "origin/$BRANCH"
git log --oneline -1

compose() {
  docker compose --env-file .env -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml --profile core "$@"
}

echo "==> Сборка и запуск"
compose up -d --build --wait --remove-orphans
docker image prune -f >/dev/null

domain="$(grep -E '^DOMAIN=' .env | cut -d= -f2)"
echo "==> Проверка https://$domain"
for i in $(seq 1 30); do
  if curl -fsS -o /dev/null "https://$domain/" && curl -fsS -o /dev/null -X POST "https://$domain/api/auth/login" \
      -H 'Content-Type: application/json' -d '{"login":"intern.anna","password":"demo"}'; then
    echo "Стенд работает: https://$domain"
    exit 0
  fi
  sleep 5
done
echo "Стенд не ответил по https://$domain — смотрите: docker compose ... logs caddy core web" >&2
compose ps
exit 1
