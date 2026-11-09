FROM golang:1.24-alpine AS build
ARG BUILD_TAGS=
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -tags "${BUILD_TAGS}" -trimpath -ldflags="-s -w" -o /out/mcp-absence ./cmd/mcp-absence

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mcp-absence /app/mcp-absence
EXPOSE 8101
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD ["/app/mcp-absence", "-healthcheck"]
ENTRYPOINT ["/app/mcp-absence"]
