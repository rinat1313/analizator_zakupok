package checklist_test

import (
	"path/filepath"
	"testing"

	"github.com/rinat1313/analizator_zakupok/internal/checklist"
)

func TestLoadDefault(t *testing.T) {
	dir := filepath.Join("..", "..", "configs", "checklists")
	list, err := checklist.Load(dir, "default")
	if err != nil {
		t.Fatal(err)
	}
	if list.ID != "default" {
		t.Fatalf("id=%q", list.ID)
	}
	if len(list.Items) < 3 {
		t.Fatalf("items=%d", len(list.Items))
	}
}

func TestListIDs(t *testing.T) {
	dir := filepath.Join("..", "..", "configs", "checklists")
	ids, err := checklist.ListIDs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 2 {
		t.Fatalf("expected >=2 checklists, got %v", ids)
	}
}
