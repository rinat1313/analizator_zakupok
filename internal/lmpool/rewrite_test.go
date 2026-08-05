package lmpool

import (
	"os"
	"testing"

	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
)

func TestRewriteDockerLocalhost(t *testing.T) {
	t.Setenv("ZAKUPKI_IN_DOCKER", "1")
	t.Setenv("LM_STUDIO_REWRITE_LOCALHOST", "")
	in := "http://127.0.0.1:49928/v1"
	got := rewriteDockerLocalhost(in)
	want := "http://host.docker.internal:49928/v1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	os.Unsetenv("ZAKUPKI_IN_DOCKER")
	t.Setenv("LM_STUDIO_REWRITE_LOCALHOST", "0")
	t.Setenv("ZAKUPKI_IN_DOCKER", "1")
	if rewriteDockerLocalhost(in) != in {
		t.Fatalf("rewrite should be disabled")
	}
}

func TestEnvMaxParallelDefault(t *testing.T) {
	t.Setenv("LM_MAX_PARALLEL", "")
	if got := envMaxParallel(); got != 4 {
		t.Fatalf("default max parallel: got %d want 4", got)
	}
	t.Setenv("LM_MAX_PARALLEL", "8")
	if got := envMaxParallel(); got != 8 {
		t.Fatalf("got %d want 8", got)
	}
}

func TestMaxParallelUsesHealthyNotCPU(t *testing.T) {
	t.Setenv("LM_MAX_PARALLEL", "4")
	p := &Pool{}
	for i := 0; i < 4; i++ {
		s := &slot{}
		s.healthy.Store(true)
		p.slots = append(p.slots, s)
	}
	if got := p.MaxParallel(); got != 4 {
		t.Fatalf("MaxParallel=%d want 4", got)
	}
	p.slots[0].healthy.Store(false)
	if got := p.MaxParallel(); got != 3 {
		t.Fatalf("MaxParallel=%d want 3", got)
	}
}


func TestLoadYamlOnlyNoEnvDefault(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/lm.yaml"
	content := `endpoints:
  - base_url: http://192.168.1.124:1234/v1
    name: lm-124
    model: qwen/qwen3-8b
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, lmstudio.Options{
		BaseURL: "http://host.docker.internal:1234/v1",
		Model:   "should-not-appear",
		APIKey:  "lm-studio",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.slots) != 1 {
		t.Fatalf("slots=%d want 1 (yaml only)", len(p.slots))
	}
	if p.slots[0].cfg.Name != "lm-124" {
		t.Fatalf("name=%q", p.slots[0].cfg.Name)
	}
}
