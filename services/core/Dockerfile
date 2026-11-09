FROM golang:1.24-alpine AS build
ARG BUILD_TAGS=demo
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -tags "${BUILD_TAGS}" -trimpath -ldflags="-s -w" -o /out/core ./cmd/core

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/core /app/core
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD ["/app/core", "-healthcheck"]
ENTRYPOINT ["/app/core"]
