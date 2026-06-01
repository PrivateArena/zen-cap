package snippet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnippetManagerCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "snippet_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "snippets.json")

	// 1. Initialize manager
	mgr, err := NewManager(dbPath)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Delete all seeded items to start from clean slate
	for _, snip := range mgr.GetAll() {
		_, _ = mgr.Delete(snip.ID)
	}

	if len(mgr.GetAll()) != 0 {
		t.Errorf("expected 0 snippets after clearing, got %d", len(mgr.GetAll()))
	}

	// 2. Add Snippet
	s1, err := mgr.Add("Test Snippet 1", "func main() {}")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if s1.Name != "Test Snippet 1" || s1.Content != "func main() {}" {
		t.Errorf("added snippet mismatch: %+v", s1)
	}

	// Verify persistence
	mgr2, err := NewManager(dbPath)
	if err != nil {
		t.Fatalf("re-initializing NewManager failed: %v", err)
	}

	all := mgr2.GetAll()
	if len(all) != 1 {
		t.Errorf("expected 1 persisted snippet, got %d", len(all))
	}

	if all[0].Name != "Test Snippet 1" || all[0].Content != "func main() {}" {
		t.Errorf("persisted snippet mismatch: %+v", all[0])
	}

	// 3. Update Snippet
	updated, err := mgr.Update(s1.ID, "Snippet 1 Updated", "func main() { println(\"hello\") }")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !updated {
		t.Error("expected Update to return true")
	}

	allUpdated := mgr.GetAll()
	if len(allUpdated) != 1 || allUpdated[0].Name != "Snippet 1Updated" && allUpdated[0].Name != "Snippet 1 Updated" {
		t.Errorf("update not applied or mismatch: %+v", allUpdated)
	}

	// 4. Delete Snippet
	deleted, err := mgr.Delete(allUpdated[0].ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !deleted {
		t.Error("expected Delete to return true")
	}

	if len(mgr.GetAll()) != 0 {
		t.Errorf("expected 0 snippets after delete, got %d", len(mgr.GetAll()))
	}
}
