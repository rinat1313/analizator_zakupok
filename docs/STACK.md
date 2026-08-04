# Полный стек: ZakupkiParser + PostgreSQL + analizator_zakupok

Одна точка входа из этого репозитория: скрипты клонируют парсер, поднимают БД и анализатор, затем можно выгрузить тендер и сразу получить AI-анализ.

```
LM Studio (хост / LAN :1234)
        ▲
        │ OpenAI-compatible /v1
┌───────┴────────┐     ┌──────────────┐
│  analizator    │────▶│  PostgreSQL  │
│  :8088         │     │  :5432       │
└───────▲────────┘     └──────────────┘
        │ analyze-url
┌───────┴────────┐
│ ZakupkiParser  │  →  DataCode/result/{reg}/…
│ (one-shot)     │
└────────────────┘
```

## Требования

1. **Docker Desktop** (или Engine) + Compose v2  
2. **LM Studio** с загруженной моделью и Local Server на порту **1234**  
3. Git, curl  
4. Доступ в интернет (ЕИС + клон репозитория парсера)

## Быстрый старт

```bash
git clone https://github.com/rinat1313/analizator_zakupok.git
cd analizator_zakupok

# укажите id модели из LM Studio
cp .env.example .env
# отредактируйте: LM_STUDIO_MODEL=...

./scripts/up.sh          # postgres + analizator + клон ZakupkiParser в .deps/
./scripts/parse.sh 1     # 1 тендер из CSV → анализ
```

Проверки:

```bash
curl -s http://127.0.0.1:8088/health
# "lm_studio":"ok"

./scripts/check_lmstudio.sh
```

Остановка:

```bash
./scripts/down.sh
```

## Что поднимается

| Сервис | Порт | Назначение |
|--------|------|------------|
| `postgres` | 5432 | БД `zakupki` / user `zakupki` / pass `zakupki` |
| `analizator` | 8088 | REST API анализа |
| `parser` | — | one-shot (profile `parser`), не демон |

Каталог тендеров (общий volume):

```
.deps/ZakupkiParser/DataCode/result/{regNumber}/
  valid_info/
  valid_doc/
  analysis/analysis.json
```

Клон парсера берётся с ветки `cursor/analizator-zakupok-e602` (там есть `-analyze-url` и интеграция). Переопределение:

```bash
ZAKUPKI_REF=main ./scripts/up.sh   # только если флаг уже в main
```

## LM Studio

### Тот же ПК, что Docker

В `.env` для `go run` можно `http://127.0.0.1:1234/v1`.  
`up.sh` для контейнеров подставит `http://host.docker.internal:1234/v1`, если видит localhost в `.env`.

### Другой ПК в LAN

```bash
cp .env.lan.example .env
# LM_STUDIO_BASE_URL=http://192.168.x.x:1234/v1
# LM_STUDIO_MODEL=...

# или:
LM_STUDIO_LAN_IP=192.168.1.50 LM_STUDIO_MODEL=my-model ./scripts/up.sh
```

В LM Studio включите **Serve on Local Network**.

## Только анализ уже выгруженного тендера

```bash
curl -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"reg_number":"0334500000125000001","checklist_id":"default"}'

curl -s http://127.0.0.1:8088/api/v1/analysis/0334500000125000001
```

## Парсер без лимита / другой чек-лист

```bash
./scripts/parse.sh 5 quick   # 5 тендеров, checklist quick
./scripts/parse.sh 0 default # все номера из CSV
```

CSV по умолчанию: `.deps/ZakupkiParser/DataCode/data/tenders.csv`.

## Чек-листы и промпты

См. [INTEGRATOR.md](INTEGRATOR.md) — правки в `configs/checklists/` и `configs/prompts/` подхватываются без пересборки образа (volume `./configs`).

## Типичные проблемы

| Симптом | Что сделать |
|---------|-------------|
| `"lm_studio":"unavailable"` | Server в LM Studio, модель, URL/порт, фаервол LAN |
| `parser` не собирается | Сначала `./scripts/up.sh` (нужен `.deps/…/DataCode`) |
| Пустой analysis / долго | Модель слабая или таймаут — смотрите `docker compose logs -f analizator` |
| ЕИС / SSL ошибки парсера | В образе уже insecure TLS; проверьте сеть и CSV-номера |

## Связанные репозитории

- Этот модуль: https://github.com/rinat1313/analizator_zakupok  
- Парсер: https://github.com/rinat1313/ZakupkiParser (ветка интеграции `cursor/analizator-zakupok-e602`)
