package lmpool_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/lmpool"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
)

func TestLoadSingleEndpointIgnoresExtras(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lm.yaml")
	content := []byte(`
endpoints:
  - base_url: http://127.0.0.1:1234/v1
    name: a
    model: m1
  - base_url: http://127.0.0.1:1235/v1
    name: b
    model: m2
health_interval_sec: 30
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := lmpool.Load(path, lmstudio.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxParallel() != 1 {
		t.Fatalf("max_parallel=%d", p.MaxParallel())
	}
	st := p.Status()
	if st.Total != 1 || st.MaxParallel != 1 || st.Mode != "single_exclusive" {
		t.Fatalf("status=%+v", st)
	}
	if p.BaseURL() != "http://127.0.0.1:1234/v1" {
		t.Fatalf("base=%s", p.BaseURL())
	}

	p2, err := lmpool.Load(path, lmstudio.Options{
		BaseURL: "http://10.0.0.5:1234/v1",
		Model:   "env-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p2.BaseURL() != "http://10.0.0.5:1234/v1" {
		t.Fatalf("base=%s", p2.BaseURL())
	}
	if p2.Model() != "env-model" {
		t.Fatalf("model=%s", p2.Model())
	}
}

func TestTryHoldExclusive(t *testing.T) {
	p, err := lmpool.Load("", lmstudio.Options{BaseURL: "http://127.0.0.1:1/v1", Model: "x"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ctx1, release, ok := p.TryHold(ctx)
	if !ok {
		t.Fatal("first hold failed")
	}
	defer release()

	if _, _, ok := p.TryHold(ctx); ok {
		t.Fatal("second hold must fail")
	}

	_, _, err = p.ChatMaxTokens(context.Background(), nil, 8)
	if err == nil || !strings.Contains(err.Error(), "занята") {
		t.Fatalf("expected busy error, got %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, e := p.ChatMaxTokens(ctx1, []lmstudio.Message{
			{Role: "user", Content: "hi"},
		}, 8)
		done <- e
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("owner chat blocked unexpectedly")
	}

	if p.Status().Busy != 1 {
		t.Fatal("expected busy while held")
	}
	release()
	if p.Status().Busy != 0 {
		t.Fatal("busy leaked after release")
	}
}
