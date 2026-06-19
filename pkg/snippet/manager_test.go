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
		if snip.Smart == "" {
			_, _ = mgr.Delete(snip.ID)
		}
	}

	var userSnippets []Snippet
	for _, snip := range mgr.GetAll() {
		if snip.Smart == "" {
			userSnippets = append(userSnippets, snip)
		}
	}

	if len(userSnippets) != 0 {
		t.Errorf("expected 0 snippets after clearing, got %d", len(userSnippets))
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
	var userSnippets2 []Snippet
	for _, snip := range all {
		if snip.Smart == "" {
			userSnippets2 = append(userSnippets2, snip)
		}
	}
	if len(userSnippets2) != 1 {
		t.Errorf("expected 1 persisted snippet, got %d", len(userSnippets2))
	}

	if userSnippets2[0].Name != "Test Snippet 1" || userSnippets2[0].Content != "func main() {}" {
		t.Errorf("persisted snippet mismatch: %+v", userSnippets2[0])
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
	var userSnippetsUpdated []Snippet
	for _, snip := range allUpdated {
		if snip.Smart == "" {
			userSnippetsUpdated = append(userSnippetsUpdated, snip)
		}
	}
	if len(userSnippetsUpdated) != 1 || userSnippetsUpdated[0].Name != "Snippet 1Updated" && userSnippetsUpdated[0].Name != "Snippet 1 Updated" {
		t.Errorf("update not applied or mismatch: %+v", userSnippetsUpdated)
	}

	// 4. Delete Snippet
	deleted, err := mgr.Delete(userSnippetsUpdated[0].ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !deleted {
		t.Error("expected Delete to return true")
	}

	var userSnippetsDeleted []Snippet
	for _, snip := range mgr.GetAll() {
		if snip.Smart == "" {
			userSnippetsDeleted = append(userSnippetsDeleted, snip)
		}
	}
	if len(userSnippetsDeleted) != 0 {
		t.Errorf("expected 0 snippets after delete, got %d", len(userSnippetsDeleted))
	}
}
