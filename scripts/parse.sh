#!/usr/bin/env bash
# Запускает ZakupkiParser (one-shot) с вызовом анализа после выгрузки.
#
# Использование:
#   ./scripts/parse.sh              # 1 тендер (по умолчанию)
#   ./scripts/parse.sh 3            # первые 3 из CSV
#   ./scripts/parse.sh 1 quick      # чек-лист quick
#   ./scripts/parse.sh 0 default    # все из CSV + checklist default
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LIMIT="${1:-1}"
CHECKLIST="${2:-default}"

DEPS_DIR="${DEPS_DIR:-$ROOT/.deps/ZakupkiParser}"
RESULT_DIR="$DEPS_DIR/DataCode/result"

if [[ ! -d "$DEPS_DIR/DataCode" ]]; then
  echo "Сначала запустите ./scripts/up.sh (нужен клон ZakupkiParser в .deps/)" >&2
  exit 1
fi

# Стек должен быть уже поднят
if ! curl -fsS --max-time 2 "http://127.0.0.1:8088/health" >/dev/null 2>&1; then
  echo "Analizator не отвечает на :8088 — сначала ./scripts/up.sh" >&2
  exit 1
fi

export TENDERS_HOST_PATH="${TENDERS_HOST_PATH:-$RESULT_DIR}"
export PARSER_CONTEXT="${PARSER_CONTEXT:-$DEPS_DIR/DataCode}"
export PARSER_DATA_PATH="${PARSER_DATA_PATH:-$DEPS_DIR/DataCode/data}"

ARGS=(-result /app/result -analyze-url http://analizator:8088 -checklist "$CHECKLIST")
if [[ "$LIMIT" != "0" ]]; then
  ARGS+=(-limit "$LIMIT")
fi

echo "==> parser ${ARGS[*]}"
docker compose --profile parser run --rm parser "${ARGS[@]}"

echo
echo "Готово. Результаты:"
echo "  файлы : $TENDERS_HOST_PATH/<reg>/analysis/analysis.json"
echo "  API   : curl -s http://127.0.0.1:8088/api/v1/analysis/<reg>"
