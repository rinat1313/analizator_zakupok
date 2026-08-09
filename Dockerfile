# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/analizator ./cmd/analizator

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl
WORKDIR /app
COPY --from=build /out/analizator /app/analizator
COPY configs /app/configs
# В Docker LM Studio на хосте доступна как host.docker.internal (не 127.0.0.1 контейнера).
ENV HTTP_ADDR=:8088 \
    TENDERS_ROOT=/data/tenders \
    CHECKLISTS_DIR=/app/configs/checklists \
    PROMPTS_DIR=/app/configs/prompts \
    LM_STUDIO_BASE_URL=http://host.docker.internal:1234/v1 \
    LM_STUDIO_MODEL=local-model \
    LM_STUDIO_API_KEY=lm-studio
EXPOSE 8088
VOLUME ["/data/tenders"]
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=10 \
  CMD curl -fsS http://127.0.0.1:8088/health || exit 1
ENTRYPOINT ["/app/analizator"]
