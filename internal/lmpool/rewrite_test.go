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
	if got := envMaxParallel(); got != 1 {
		t.Fatalf("default max parallel: got %d want 1", got)
	}
	t.Setenv("LM_MAX_PARALLEL", "8")
	if got := envMaxParallel(); got != 8 {
		t.Fatalf("got %d want 8", got)
	}
}

func TestMaxParallelUsesHealthyNotCPU(t *testing.T) {
	t.Setenv("LM_MAX_PARALLEL", "1")
	p := &Pool{}
	for i := 0; i < 4; i++ {
		s := &slot{}
		s.healthy.Store(true)
		p.slots = append(p.slots, s)
	}
	// Cap at LM_MAX_PARALLEL=1 even if 4 healthy slots exist.
	if got := p.MaxParallel(); got != 1 {
		t.Fatalf("MaxParallel=%d want 1", got)
	}
	p.slots[0].healthy.Store(false)
	p.slots[1].healthy.Store(false)
	p.slots[2].healthy.Store(false)
	if got := p.MaxParallel(); got != 1 {
		t.Fatalf("MaxParallel=%d want 1 (one healthy left, cap 1)", got)
	}
	p.slots[3].healthy.Store(false)
	if got := p.MaxParallel(); got != 0 {
		t.Fatalf("MaxParallel=%d want 0", got)
	}
}
