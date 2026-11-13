# Сервис агента (владелец A). Контекст сборки — корень репозитория (нужны data/kb и data/checklist):
#   docker build -f services/agent/Dockerfile -t aibuddy-agent .
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1
WORKDIR /app

COPY services/agent/requirements.txt ./requirements.txt
RUN pip install -r requirements.txt

COPY services/agent/agent ./agent
COPY services/agent/prompts ./prompts
COPY services/agent/fixtures ./fixtures
COPY services/agent/agent.yaml ./agent.yaml
COPY data/kb /data/kb
COPY data/checklist /data/checklist

ENV AGENT_PORT=8090 AGENT_CONFIG=/app/agent.yaml KB_DIR=/data/kb CHECKLIST_PATH=/data/checklist/checklist.yaml \
    MOCK_FIXTURES_PATH=/app/fixtures/mock_services.yaml
EXPOSE 8090
USER nobody

HEALTHCHECK --interval=10s --timeout=3s --retries=10 \
  CMD python -c "import urllib.request,os; urllib.request.urlopen(f'http://127.0.0.1:{os.environ.get(\"AGENT_PORT\",\"8090\")}/healthz')"

# Один воркер с потоками: потоки SSE упираются в ввод-вывод; моки в процессе хранят состояние в процессе (чтобы
# масштабироваться на несколько воркеров, используйте Redis + MCP-серверы).
CMD ["sh", "-c", "exec gunicorn -k gthread -w 1 --threads 16 --timeout 120 -b 0.0.0.0:${AGENT_PORT} 'agent.app:create_app()'"]
