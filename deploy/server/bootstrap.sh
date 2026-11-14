#!/usr/bin/env bash
# Первичная настройка свежего VPS (Ubuntu 22.04/24.04) под стенд AI-buddy. Запускать один раз от root:
#   curl -fsSL https://raw.githubusercontent.com/Eliz-Kory/AI-Buddy/main/deploy/server/bootstrap.sh | sudo bash
# Необязательные переменные: REPO (URL репозитория), BRANCH (по умолчанию main),
# DEPLOY_PUBKEY (публичный SSH-ключ для деплоя из GitHub Actions), DOMAIN (по умолчанию <ip-с-дефисами>.sslip.io),
# DOCKER_MIRROR (зеркало Docker Hub; по умолчанию зеркало Timeweb — Docker Hub недоступен с российских IP; "" — без зеркала).
set -euo pipefail

REPO="${REPO:-https://github.com/Eliz-Kory/AI-Buddy.git}"
BRANCH="${BRANCH:-main}"
APP_DIR=/opt/aibuddy
DEPLOY_USER=deploy

echo "==> Пакеты и Docker"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl git ufw openssl >/dev/null
if ! command -v docker >/dev/null; then
  # официальный установщик; если download.docker.com недоступен — пакеты Ubuntu (compose v2 из docker-compose-v2)
  curl -fsSL https://get.docker.com | sh || apt-get install -y -qq docker.io docker-compose-v2
fi
DOCKER_MIRROR="${DOCKER_MIRROR-https://dockerhub.timeweb.cloud}"
if [ -n "$DOCKER_MIRROR" ] && [ ! -f /etc/docker/daemon.json ]; then
  mkdir -p /etc/docker
  printf '{\n  "registry-mirrors": ["%s"]\n}\n' "$DOCKER_MIRROR" > /etc/docker/daemon.json
  systemctl restart docker
fi
systemctl enable --now docker
docker compose version

echo "==> Файрвол: открыты только SSH, 80 и 443"
ufw allow OpenSSH >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw allow 443/udp >/dev/null
ufw --force enable >/dev/null

echo "==> Своп 2 ГБ (сборка образов на сервере с 4 ГБ памяти)"
if ! swapon --show | grep -q /swapfile; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile
  grep -q /swapfile /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

echo "==> Пользователь $DEPLOY_USER для деплоя"
id "$DEPLOY_USER" >/dev/null 2>&1 || useradd -m -s /bin/bash "$DEPLOY_USER"
usermod -aG docker "$DEPLOY_USER"
install -d -m 700 -o "$DEPLOY_USER" -g "$DEPLOY_USER" "/home/$DEPLOY_USER/.ssh"
touch "/home/$DEPLOY_USER/.ssh/authorized_keys"
if [ -n "${DEPLOY_PUBKEY:-}" ] && ! grep -qF "$DEPLOY_PUBKEY" "/home/$DEPLOY_USER/.ssh/authorized_keys"; then
  echo "$DEPLOY_PUBKEY" >> "/home/$DEPLOY_USER/.ssh/authorized_keys"
fi
chown "$DEPLOY_USER:$DEPLOY_USER" "/home/$DEPLOY_USER/.ssh/authorized_keys"
chmod 600 "/home/$DEPLOY_USER/.ssh/authorized_keys"

echo "==> Код в $APP_DIR"
if [ ! -d "$APP_DIR/.git" ]; then
  install -d -o "$DEPLOY_USER" -g "$DEPLOY_USER" "$APP_DIR"
  sudo -u "$DEPLOY_USER" git clone --branch "$BRANCH" "$REPO" "$APP_DIR"
fi

echo "==> Настройки стенда ($APP_DIR/.env)"
if [ ! -f "$APP_DIR/.env" ]; then
  ip="$(curl -fsS -4 https://api.ipify.org || hostname -I | awk '{print $1}')"
  domain="${DOMAIN:-${ip//./-}.sslip.io}"
  umask 077
  cat > "$APP_DIR/.env" <<ENV
# Стенд AI-buddy. Файл создан bootstrap.sh, в git не попадает.
DOMAIN=$domain
JWT_SECRET=$(openssl rand -hex 32)
CONFIRMATION_HMAC_SECRET=$(openssl rand -hex 32)
LLM_DEFAULT_PROVIDER=stub
AVIBE_PULL=false
ENV
  chown "$DEPLOY_USER:$DEPLOY_USER" "$APP_DIR/.env"
fi

echo "==> Первый запуск (сборка займёт 5–10 минут)"
sudo -u "$DEPLOY_USER" BRANCH="$BRANCH" bash "$APP_DIR/deploy/server/deploy.sh"
