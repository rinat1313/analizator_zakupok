#!/usr/bin/env bash
# Останавливает стек (postgres + analizator). Данные Postgres и тендеры сохраняются.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

DEPS_DIR="${DEPS_DIR:-$ROOT/.deps/ZakupkiParser}"
RESULT_DIR="$DEPS_DIR/DataCode/result"
export TENDERS_HOST_PATH="${TENDERS_HOST_PATH:-$RESULT_DIR}"
export PARSER_CONTEXT="${PARSER_CONTEXT:-$DEPS_DIR/DataCode}"
export PARSER_DATA_PATH="${PARSER_DATA_PATH:-$DEPS_DIR/DataCode/data}"

docker compose --profile parser down "$@"
echo "Стек остановлен. Тома pgdata и каталог тендеров сохранены."
