package analyzer

import (
	"strings"
	"testing"
)

func TestNormalizeRecommendation(t *testing.T) {
	if normalizeRecommendation("да") != "participate" {
		t.Fatal()
	}
	if normalizeRecommendation("Нет") != "skip" {
		t.Fatal()
	}
	if normalizeRecommendation("с оговорками") != "caution" {
		t.Fatal()
	}
}

func TestEnsureSummaryPrefix(t *testing.T) {
	if !strings.HasPrefix(ensureSummaryPrefix("текст", "participate"), "Да:") {
		t.Fatal()
	}
	if !strings.HasPrefix(ensureSummaryPrefix("текст", "skip"), "Нет:") {
		t.Fatal()
	}
	if got := ensureSummaryPrefix("С оговорками: уже", "caution"); got != "С оговорками: уже" {
		t.Fatalf("got %q", got)
	}
}

func TestParseSynthJSONUIFields(t *testing.T) {
	out := parseSynthJSON(`{"recommendation":"participate","score":0.8,"summary":"подходит по срокам"}`)
	if out.Recommendation != "participate" {
		t.Fatalf("rec=%s", out.Recommendation)
	}
	if !strings.HasPrefix(out.Summary, "Да:") {
		t.Fatalf("summary=%q", out.Summary)
	}
	if out.Risks == nil || out.Actions == nil {
		t.Fatal("risks/actions must be non-nil for UI JSON")
	}
}
