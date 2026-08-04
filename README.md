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

> Для интегратора: пошаговая инструкция LAN + чек-листы + промпты — [`docs/INTEGRATOR.md`](docs/INTEGRATOR.md).

```bash
# 1) LM Studio: Local Server на :1234 (для LAN — Serve on Local Network)
# 2) скопируйте env
cp .env.example .env          # тот же ПК
# или: cp .env.lan.example .env  # LM Studio на другом IP в сети

export LM_STUDIO_BASE_URL=http://127.0.0.1:1234/v1   # или http://192.168.x.x:1234/v1
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

Полный стек (parser + PostgreSQL + analizator) поднимается **из репозитория парсера**, не из этого модуля.

Ветка/PR с готовым `docker-compose.yml`:  
https://github.com/rinat1313/ZakupkiParser/pull/1

```bash
git clone https://github.com/rinat1313/ZakupkiParser.git
cd ZakupkiParser
git fetch origin pull/1/head:stack && git checkout stack

# LM Studio Local Server :1234, модель загружена
export LM_STUDIO_MODEL=<id-модели>
docker compose up -d --build
docker compose run --rm parser -limit 1 -analyze-url http://analizator:8088
```

Анализ уже выгруженного тендера:

```bash
curl -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"reg_number":"<номер>"}'
```

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

## Промпты LM Studio

Тексты system-ролей (без правки Go-кода):

- `configs/prompts/item_system.txt` — анализ пункта чек-листа  
- `configs/prompts/synthesize_system.txt` — итоговая рекомендация  

Инструкция: [`docs/INTEGRATOR.md`](docs/INTEGRATOR.md).

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
| `CHECKLISTS_DIR` | `configs/checklists` | каталог YAML чек-листов |
| `PROMPTS_DIR` | `configs/prompts` | system-промпты для LM Studio |
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

## Связь с ZakupkiParser

Этот репозиторий — отдельный модуль. Интеграция с парсером (общий `docker-compose`, флаг `-analyze-url`, создание `analysis/` при выгрузке) живёт в [ZakupkiParser](https://github.com/rinat1313/ZakupkiParser) (PR/ветка `cursor/analizator-zakupok-e602`).

Каталог тендеров монтируйте из парсера, например:

```bash
# рядом с клоном ZakupkiParser:
export TENDERS_ROOT=../ZakupkiParser/DataCode/result
```
