package lmpool

import (
	"os"
	"testing"
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
