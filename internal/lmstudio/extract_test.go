package lmstudio

import "testing"

func TestExtractAssistantTextFromReasoning(t *testing.T) {
	got := ExtractAssistantText("", `{"status":"warn","score":0.4,"notes":"Нужна лицензия","flags":["лицензия"]}`)
	if got == "" || got[0] != '{' {
		t.Fatalf("expected JSON from reasoning, got %q", got)
	}
}

func TestExtractAssistantTextStripsThink(t *testing.T) {
	in := "<think>долго думаю</think>\n{\"status\":\"ok\",\"score\":0.8,\"notes\":\"ok\",\"flags\":[]}"
	got := ExtractAssistantText(in, "")
	if got[0] != '{' {
		t.Fatalf("got %q", got)
	}
}

func TestExtractEmpty(t *testing.T) {
	if ExtractAssistantText("", "") != "" {
		t.Fatal("expected empty")
	}
}
