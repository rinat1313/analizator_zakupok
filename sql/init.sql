-- Раздел анализа тендеров (дублирует analysis/analysis.json).
CREATE TABLE IF NOT EXISTS tender_analyses (
  reg_number     TEXT PRIMARY KEY,
  law            TEXT,
  status         TEXT NOT NULL,
  checklist_id   TEXT,
  recommendation TEXT,
  score          DOUBLE PRECISION DEFAULT 0,
  summary        TEXT,
  result_json    JSONB NOT NULL,
  analyzed_at    TIMESTAMPTZ,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tender_analyses_status ON tender_analyses (status);
CREATE INDEX IF NOT EXISTS idx_tender_analyses_recommendation ON tender_analyses (recommendation);
