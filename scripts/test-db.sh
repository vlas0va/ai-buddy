#!/usr/bin/env bash
# Disposable Postgres for Go tests.  Usage: scripts/test-db.sh up|down|url
#   up   - start postgres:16 on 127.0.0.1:55432 and wait until it is healthy
#   down - stop it and drop its (tmpfs) data
#   url  - print TEST_DATABASE_URL
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/deploy/docker-compose.test.yml"
URL="postgres://buddy:buddy@127.0.0.1:55432/postgres?sslmode=disable"

need_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "Docker не найден / Docker CLI not found." >&2
    echo "Установите Docker Desktop или задайте TEST_DATABASE_URL на свой Postgres 16 (суперпользователь)." >&2
    echo "Install Docker Desktop or set TEST_DATABASE_URL to your own Postgres 16 superuser connection." >&2
    exit 1
  fi
  if ! docker info >/dev/null 2>&1; then
    echo "Docker не запущен / Docker daemon is not running. Запустите Docker Desktop и повторите." >&2
    echo "Start Docker Desktop and retry." >&2
    exit 1
  fi
}

case "${1:-}" in
  up)
    need_docker
    docker compose -f "$COMPOSE_FILE" up -d --wait >&2
    echo "$URL"
    ;;
  down)
    need_docker
    docker compose -f "$COMPOSE_FILE" down -v >&2
    ;;
  url)
    echo "$URL"
    ;;
  *)
    echo "usage: scripts/test-db.sh up|down|url" >&2
    exit 2
    ;;
esac
