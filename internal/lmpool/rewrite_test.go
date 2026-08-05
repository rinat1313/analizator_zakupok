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
