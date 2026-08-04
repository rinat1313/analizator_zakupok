#!/usr/bin/env bash
# Поднимает PostgreSQL + analizator_zakupok и готовит ZakupkiParser.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

ZAKUPKI_REPO="${ZAKUPKI_REPO:-https://github.com/rinat1313/ZakupkiParser.git}"
# Ветка с docker-compose / -analyze-url (пока не влита в main).
ZAKUPKI_REF="${ZAKUPKI_REF:-cursor/analizator-zakupok-e602}"
DEPS_DIR="${DEPS_DIR:-$ROOT/.deps/ZakupkiParser}"

echo "==> ZakupkiParser → $DEPS_DIR ($ZAKUPKI_REF)"
if [[ -d "$DEPS_DIR/.git" ]]; then
  git -C "$DEPS_DIR" fetch --depth 1 origin "$ZAKUPKI_REF" 2>/dev/null || true
  git -C "$DEPS_DIR" checkout -q "$ZAKUPKI_REF" 2>/dev/null \
    || git -C "$DEPS_DIR" checkout -q -B "$ZAKUPKI_REF" "FETCH_HEAD"
  git -C "$DEPS_DIR" pull --ff-only origin "$ZAKUPKI_REF" 2>/dev/null || true
else
  mkdir -p "$(dirname "$DEPS_DIR")"
  git clone --depth 1 --branch "$ZAKUPKI_REF" "$ZAKUPKI_REPO" "$DEPS_DIR"
fi

RESULT_DIR="$DEPS_DIR/DataCode/result"
mkdir -p "$RESULT_DIR"

if [[ ! -f "$ROOT/.env" ]]; then
  if [[ -n "${LM_STUDIO_LAN_IP:-}" ]]; then
    cp "$ROOT/.env.lan.example" "$ROOT/.env"
    # shellcheck disable=SC2016
    sed -i.bak "s|http://192.168.1.50:1234/v1|http://${LM_STUDIO_LAN_IP}:1234/v1|" "$ROOT/.env" || true
    rm -f "$ROOT/.env.bak"
    echo "==> Создан .env из .env.lan.example (LAN IP=$LM_STUDIO_LAN_IP)"
  else
    cp "$ROOT/.env.example" "$ROOT/.env"
    echo "==> Создан .env из .env.example — укажите LM_STUDIO_MODEL"
  fi
fi

# Для Docker на том же хосте URL из .env.example (127.0.0.1) недоступен из контейнера.
if grep -q 'LM_STUDIO_BASE_URL=http://127.0.0.1:1234/v1' "$ROOT/.env" 2>/dev/null; then
  if [[ -z "${LM_STUDIO_BASE_URL:-}" ]]; then
    export LM_STUDIO_BASE_URL="http://host.docker.internal:1234/v1"
    echo "==> LM_STUDIO_BASE_URL → host.docker.internal (Docker → LM Studio на хосте)"
  fi
fi

# Подхватываем модель из .env, если не задана в окружении.
if [[ -z "${LM_STUDIO_MODEL:-}" ]] && [[ -f "$ROOT/.env" ]]; then
  # shellcheck disable=SC1091
  set -a
  # shellcheck disable=SC1090
  source "$ROOT/.env"
  set +a
fi

export TENDERS_HOST_PATH="${TENDERS_HOST_PATH:-$RESULT_DIR}"
export PARSER_CONTEXT="${PARSER_CONTEXT:-$DEPS_DIR/DataCode}"
export PARSER_DATA_PATH="${PARSER_DATA_PATH:-$DEPS_DIR/DataCode/data}"

if ! command -v docker >/dev/null 2>&1; then
  echo "Ошибка: нужен Docker Desktop / Docker Engine + Compose v2" >&2
  exit 1
fi

echo "==> docker compose up (postgres + analizator)"
echo "    TENDERS_HOST_PATH=$TENDERS_HOST_PATH"
echo "    LM_STUDIO_BASE_URL=${LM_STUDIO_BASE_URL:-из .env / default}"
echo "    LM_STUDIO_MODEL=${LM_STUDIO_MODEL:-local-model}"

docker compose up -d --build

echo "==> Ждём health analizator…"
ok=0
for _ in $(seq 1 60); do
  if curl -fsS --max-time 2 "http://127.0.0.1:8088/health" >/tmp/analizator_health.json 2>/dev/null; then
    ok=1
    break
  fi
  sleep 1
done

if [[ "$ok" -ne 1 ]]; then
  echo "Предупреждение: /health не ответил за 60с. Логи: docker compose logs -f analizator" >&2
else
  echo "==> health:"
  cat /tmp/analizator_health.json
  echo
  if grep -q '"lm_studio":"unavailable"' /tmp/analizator_health.json 2>/dev/null; then
    echo
    echo "⚠ LM Studio недоступен из контейнера."
    echo "  1) Запустите Local Server в LM Studio (порт 1234, модель загружена)"
    echo "  2) Проверьте LM_STUDIO_BASE_URL и LM_STUDIO_MODEL в .env"
    echo "  3) LAN: LM_STUDIO_LAN_IP=192.168.x.x ./scripts/up.sh"
    echo "  Подробнее: docs/STACK.md"
  fi
fi

cat <<EOF

✅ Стек запущен
  PostgreSQL : localhost:5432  (zakupki / zakupki / zakupki)
  Analizator : http://127.0.0.1:8088
  Тендеры    : $TENDERS_HOST_PATH

Дальше:
  ./scripts/parse.sh 1          # выгрузить 1 тендер и сразу проанализировать
  ./scripts/check_lmstudio.sh   # проверка LM Studio с хоста
  curl -s http://127.0.0.1:8088/health
  ./scripts/down.sh             # остановить

Документация: docs/STACK.md
EOF
