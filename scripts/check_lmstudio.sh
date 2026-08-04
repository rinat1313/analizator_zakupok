#!/usr/bin/env bash
# Проверка доступности LM Studio (LAN или localhost).
set -euo pipefail
BASE="${LM_STUDIO_BASE_URL:-http://127.0.0.1:1234/v1}"
BASE="${BASE%/}"
echo "GET $BASE/models"
curl -fsS --max-time 5 "$BASE/models" | head -c 500
echo
echo "OK: LM Studio отвечает"
