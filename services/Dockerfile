# Один рецепт образа для каждого бинарника Go. Контекст сборки — корень репозитория.
#   docker build -f services/Dockerfile --build-arg CMD=core -t aibuddy-core .
#   docker build -f services/Dockerfile --build-arg CMD=mcp-mocks -t aibuddy-mcp-mocks .
# BUILD_TAGS=demo включает эндпоинты только для демо (запуск cron, смена флагов); BUILD_TAGS="" — для защищённой сборки.
FROM golang:1.24-alpine AS builder
ARG CMD=core
ARG BUILD_TAGS=demo
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -tags "$BUILD_TAGS" -trimpath -ldflags="-s -w" -o /out/app ./services/cmd/$CMD

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/app /app/app
ENTRYPOINT ["/app/app"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 CMD ["/app/app", "-healthcheck"]
