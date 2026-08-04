#!/usr/bin/env bash
# Публикация каталога analizator_zakupok как отдельного GitHub-репозитория.
# Запускать от владельца аккаунта с правом create repository.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NAME="${REPO_NAME:-analizator_zakupok}"
OWNER="${REPO_OWNER:-rinat1313}"

cd "$ROOT"

if [[ ! -d .git ]]; then
  git init -b main
  git add .
  git commit -m "Initial commit: AI-ассистент анализа закупок (LM Studio)"
fi

if gh repo view "$OWNER/$NAME" >/dev/null 2>&1; then
  echo "Репозиторий $OWNER/$NAME уже существует"
else
  gh repo create "$OWNER/$NAME" --public \
    --description "AI-ассистент анализа тендеров (LM Studio) для ZakupkiParser" \
    --source=. --remote=origin --push
  echo "Создан и запушен https://github.com/$OWNER/$NAME"
  exit 0
fi

git remote remove origin 2>/dev/null || true
git remote add origin "https://github.com/$OWNER/$NAME.git"
git push -u origin main
echo "Запушено в https://github.com/$OWNER/$NAME"
