# analizator_zakupok

AI-ассистент анализа госзакупок для [ZakupkiParser](https://github.com/rinat1313/ZakupkiParser).

Микросервис на **Go**: читает сохранённые данные тендера (карточка + `valid_doc/*.txt`), подаёт текст в **LM Studio** частями (chunking), сверяет с **чек-листами** и пишет результат в раздел **`analysis/`** тендера (и опционально в PostgreSQL).

## Возможности

- REST API: анализ по `reg_number` или произвольному тексту
- Нарезка больших документов на перекрывающиеся фрагменты
- Отбор релевантных кусков по keywords пункта чек-листа
- Map → Reduce: анализ пунктов → итоговая рекомендация
- Сохранение в `result/{reg}/analysis/analysis.json` (+ дубль в `valid_info/`)
- Опциональная запись в СУБД (`tender_analyses`)
- Docker / docker-compose, совместный запуск с парсером

## Быстрый старт

```bash
# LM Studio: Local Server → порт 1234, загружена модель
export LM_STUDIO_BASE_URL=http://127.0.0.1:1234/v1
export LM_STUDIO_MODEL=ваш-идентификатор-модели
export TENDERS_ROOT=/path/to/ZakupkiParser/DataCode/result

go run ./cmd/analizator
```

```bash
# анализ тендера из папки парсера
curl -s -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"reg_number":"0334500000125000001","checklist_id":"default"}'

# анализ произвольного текста
curl -s -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"text":"Извещение: поставка бумаги А4, НМЦК 500000 руб...","checklist_id":"quick"}'

# результат
curl -s http://127.0.0.1:8088/api/v1/analysis/0334500000125000001
```

## Docker

```bash
docker compose up -d --build
```

Сервис слушает `:8088`. Каталог тендеров — volume `tenders_data` (или смонтируйте `../DataCode/result`).

Для доступа к LM Studio на хосте используется `host.docker.internal`.

## Совместно с ZakupkiParser

В репозитории парсера есть корневой `docker-compose.yml`: PostgreSQL + **analizator** + общий volume `DataCode/result`.

После выгрузки тендера парсером:

```bash
curl -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"reg_number":"<номер>"}'
```

Либо флаг парсера `-analyze-url http://analizator:8088` (см. ZakupkiParser).

## Структура данных тендера

```
result/{regNumber}/
  valid_info/     # tender.json, customer.json, export.json, …
  valid_doc/      # .txt для LLM
  analysis/       # ← раздел анализа
    analysis.json
  valid_info/analysis.json  # дубль для удобства
```

Пример `analysis.json`:

```json
{
  "reg_number": "…",
  "status": "completed",
  "checklist_id": "default",
  "recommendation": "caution",
  "score": 0.62,
  "summary": "…",
  "items": [ { "id": "nmck_and_finance", "status": "warn", "findings": "…" } ],
  "risks": ["…"],
  "actions": ["…"],
  "chunks_total": 28,
  "chunks_used": 12
}
```

## Чек-листы

YAML в `configs/checklists/`:

| Файл | Назначение |
|------|------------|
| `default.yaml` | Полный разбор (предмет, НМЦК, сроки, требования, документы, риски) |
| `quick.yaml` | Быстрый скрининг |

Свой чек-лист: положите `configs/checklists/my.yaml` и передайте `"checklist_id":"my"`.

## Переменные окружения

| Переменная | По умолчанию | Смысл |
|------------|--------------|--------|
| `HTTP_ADDR` | `:8088` | bind |
| `TENDERS_ROOT` | `/data/tenders` | корень `result/` парсера |
| `LM_STUDIO_BASE_URL` | `http://host.docker.internal:1234/v1` | OpenAI-compatible base |
| `LM_STUDIO_MODEL` | `local-model` | id модели в LM Studio |
| `LM_STUDIO_API_KEY` | `lm-studio` | любой непустой ключ |
| `CHUNK_SIZE` | `3500` | размер куска (руны) |
| `CHUNK_OVERLAP` | `250` | перекрытие |
| `MAX_CHUNKS` | `40` | потолок кусков на тендер |
| `CONCURRENCY` | `2` | параллельные запросы к LLM |
| `DEFAULT_CHECKLIST` | `default` | чек-лист по умолчанию |
| `DATABASE_URL` | _(пусто)_ | PostgreSQL DSN (опционально) |

## API

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/health` | liveness + ping LM Studio |
| `GET` | `/api/v1/checklists` | список чек-листов |
| `POST` | `/api/v1/analyze` | запуск анализа |
| `GET` | `/api/v1/analysis/{reg}` | получить сохранённый анализ |

## Разработка

```bash
go test ./...
go build -o bin/analizator ./cmd/analizator
```

## Публикация отдельным репозиторием

Код рассчитан на репозиторий `github.com/rinat1313/analizator_zakupok`.
Если репозиторий ещё не создан на GitHub:

```bash
# от владельца аккаунта (нужны права create repo):
gh repo create rinat1313/analizator_zakupok --public \
  --description "AI-ассистент анализа тендеров (LM Studio) для ZakupkiParser" \
  --source=. --remote=origin --push
```

Либо создайте пустой репозиторий в UI и выполните `git push -u origin main` из этой папки.
