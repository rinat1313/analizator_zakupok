# Инструкция интегратора: analizator_zakupok + LM Studio

Кратко: **правила оценки** правятся в YAML-чек-листах, **общие промпты** — в `configs/prompts/`, **LM Studio** настраивается отдельно (сервер + модель), а микросервис только указывает URL/модель через `.env`.

---

## 1. Где что настраивать (карта)

| Что нужно | Где править | Нужен ли рестарт |
|-----------|-------------|------------------|
| Правила оценки (пункты, вопросы, keywords) | `configs/checklists/*.yaml` | нет*, volume `./configs` уже смонтирован |
| System-промпт «как отвечать» для пункта | `configs/prompts/item_system.txt` | нет* |
| System-промпт итога (participate/caution/skip) | `configs/prompts/synthesize_system.txt` | нет* |
| URL LM Studio, модель, температура | `.env` / docker-compose env | да (пересоздать контейнер) |
| Загрузка модели, порт, LAN | **приложение LM Studio** на ПК с GPU | — |

\* при `docker compose` с volume `./configs:/app/configs:ro` — достаточно сохранить файл; сервис читает чек-лист на каждый запрос. Промпты кэшируются в памяти процесса — после правки `configs/prompts/` сделайте `docker compose restart analizator`.

---

## 2. Настройка LM Studio (на машине с моделью)

1. Установите [LM Studio](https://lmstudio.ai), скачайте подходящую модель (чат, лучше ≥7B с нормальным JSON).
2. Загрузите модель в чат (Load).
3. Вкладка **Developer** (или Local Server):
   - **Start Server**
   - порт по умолчанию **1234**
   - включите **Serve on Local Network** / bind `0.0.0.0` (чтобы другие ПК в LAN видели сервер)
4. Скопируйте **идентификатор модели** из LM Studio (поле Model) — его кладёте в `LM_STUDIO_MODEL`.
5. Проверка с любого ПК в сети:

```bash
curl http://<IP-машины-с-LM>:1234/v1/models
```

Должен вернуться JSON со списком моделей. Если таймаут — фаервол / Serve on Local Network выключен.

**Важно:** в UI LM Studio отдельный «промпт системы» для нашего сервиса **не нужен**. Сервис сам шлёт `system` + `user` в `/v1/chat/completions`. В LM Studio достаточно: сервер запущен + модель загружена.

---

## 3. Подключение микросервиса к LM Studio в LAN

### Вариант A — analizator на том же ПК, что LM Studio

```bash
cp .env.example .env
# LM_STUDIO_BASE_URL=http://127.0.0.1:1234/v1
# LM_STUDIO_MODEL=<id из LM Studio>
go run ./cmd/analizator
# или: docker compose up -d --build
```

В Docker на том же хосте используйте:

```env
LM_STUDIO_BASE_URL=http://host.docker.internal:1234/v1
```

### Вариант B — LM Studio на другом ПК в локальной сети (рекомендуется для «сервер с GPU»)

1. Узнайте IP ПК с LM Studio, например `192.168.1.50`.
2. В LM Studio включите Serve on Local Network.
3. На машине с analizator:

```bash
cp .env.lan.example .env
# отредактируйте:
# LM_STUDIO_BASE_URL=http://192.168.1.50:1234/v1
# LM_STUDIO_MODEL=...
docker compose up -d --build
```

Или без Docker:

```bash
export LM_STUDIO_BASE_URL=http://192.168.1.50:1234/v1
export LM_STUDIO_MODEL=ваш-model-id
export TENDERS_ROOT=/path/to/ZakupkiParser/DataCode/result
go run ./cmd/analizator
```

### Проверка связи

```bash
curl -s http://127.0.0.1:8088/health
```

В ответе должно быть `"lm_studio":"ok"`. Если `"unavailable"` — смотрите `lm_studio_error`, URL и фаервол.

---

## 4. Куда добавлять правила оценки (чек-листы)

Файл: `configs/checklists/<имя>.yaml`

Пример нового правила — пункт в `items:`:

```yaml
  - id: my_rule
    title: Краткое название правила
    description: Зачем проверяем
    prompt: >
      Конкретная инструкция модели по этому пункту.
      Что искать, что считать fail/warn.
    keywords: [ключ1, ключ2, нмцк]   # по ним выбираются куски текста
    max_chunks: 4
    weight: 1.0
```

- **`prompt`** — главное место «правил для пункта»: модель получает его как инструкцию вместе с фрагментами документов.
- **`keywords`** — не промпт, а отбор кусков большого текста (чтобы в LM Studio не слать весь том).
- Свой файл, например `configs/checklists/stroyka.yaml`, затем в запросе: `"checklist_id":"stroyka"` или `DEFAULT_CHECKLIST=stroyka`.

Готовые наборы: `default.yaml` (полный), `quick.yaml` (скрининг).

---

## 5. Куда класть общий промпт для LM Studio

Папка: `configs/prompts/`

| Файл | Роль |
|------|------|
| `item_system.txt` | System-роль на **каждый пункт** чек-листа (формат JSON, роль эксперта) |
| `synthesize_system.txt` | System-роль на **итоговую** рекомендацию |

Промпт пункта из YAML (`items[].prompt`) **дополняет** `item_system.txt`, а не заменяет его.

Меняйте тексты осторожно: сервис ждёт **строго JSON** в ответе модели. Если сломать схему — статусы/score могут стать `unknown`.

---

## 6. Типовой поток интегратора

```
ZakupkiParser → DataCode/result/{reg}/ (valid_info + valid_doc)
        ↓
analizator POST /api/v1/analyze {"reg_number":"...","checklist_id":"default"}
        ↓
chunking → LM Studio (по пунктам) → синтез
        ↓
result/{reg}/analysis/analysis.json  (+ optional PostgreSQL)
```

Пример вызова:

```bash
curl -s -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"reg_number":"0334500000125000001","checklist_id":"default"}'
```

Произвольный текст без парсера:

```bash
curl -s -X POST http://127.0.0.1:8088/api/v1/analyze \
  -H 'Content-Type: application/json' \
  -d '{"checklist_id":"quick","text":"... текст извещения ..."}'
```

Совместно с парсером (из ZakupkiParser):

```bash
go run ./cmd/parser -limit 1 -analyze-url http://127.0.0.1:8088
```

---

## 7. Чеклист приёмочного теста

1. `curl http://<lm-ip>:1234/v1/models` → OK  
2. `curl http://127.0.0.1:8088/health` → `lm_studio: ok`  
3. Есть папка тендера с `valid_info/` или передан `text`  
4. `POST /api/v1/analyze` → `status: completed`  
5. Появился файл `analysis/analysis.json` с `recommendation` и `items`

---

## 8. Частые проблемы

| Симптом | Что проверить |
|---------|----------------|
| `lm_studio unavailable` | сервер в LM Studio, IP, порт 1234, Serve on Local Network, фаервол |
| пустой/кривой JSON от модели | другая модель; упростить промпт; снизить temperature (`LM_TEMPERATURE=0.1`) |
| анализ долго / OOM | уменьшить `PAGE_CHARS`, `DOSE_PAGES`, `CONTEXT_BUDGET_CHARS` |
| `LLM занята` / HTTP 409 | уже идёт другой анализ — режим single exclusive (1 процесс на LLM) |
| «тендер не найден» | `TENDERS_ROOT` указывает на `DataCode/result` парсера |
| правила не применились | неверный `checklist_id`; опечатка в имени файла YAML |

---

## 9. Переменные окружения (минимум)

```env
LM_STUDIO_BASE_URL=http://192.168.1.50:1234/v1
LM_STUDIO_MODEL=<id модели>
TENDERS_ROOT=/path/to/DataCode/result
CHECKLISTS_DIR=configs/checklists
PROMPTS_DIR=configs/prompts
DEFAULT_CHECKLIST=default
```

---

## 10. Связка с Zakupki Platform / UI «Поисковики»

Поток UI (gateway searchers → search → core → analizator):

1. Пользователь выбирает **настройку поиска** и включает `auto_ai` на неё.
2. Поиск кладёт тендеры в core (`search_config_id`).
3. Core обрабатывает документы; когда есть `text_content`, шлёт в analizator.
4. Analizator работает в режиме **1 LLM / очередь**: параллельные запросы ждут слот
   (`phase=queued` → `prepare` → `dose` → `synthesize` → `done`), не отдают 409 «занята»
   (иначе UI рисует карточку как «ошибка / прочее»).

Тело от core:

```json
{
  "reg_number": "…",
  "text": "корпус карточки + ### Документ: …",
  "title": "…",
  "checklist_id": "<ai_config uuid>",
  "config_id": "<alias>",
  "config_name": "…",
  "system_prompt": "…",
  "user_prompt": "…",
  "rules": "…"
}
```

Ответ для UI pills (`Да` / `Нет` / `С оговорками`):

- `recommendation`: `participate|caution|skip|unknown`
- `summary`: начинается с `Да:` / `Нет:` / `С оговорками:`
- `risks`, `actions`, `items` — всегда массивы (не `null`)
- прогресс: `GET /api/v1/analyze/progress/{reg}` → `percent`, `phase` (core пишет в `ai_pct`)

