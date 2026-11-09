package registry

import (
	"path/filepath"
	"testing"
)

func TestAddRenamesAndRejectsConflicts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "projects.json")
	if err := Add(file, "blog", "/a"); err != nil {
		t.Fatal(err)
	}
	if err := Add(file, "blog", "/b"); err == nil {
		t.Fatal("expected a conflict for a domain used by another directory")
	}
	if err := Add(file, "news", "/a"); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["news"] != "/a" {
		t.Errorf("got %v", got)
	}
}

func TestRemove(t *testing.T) {
	file := filepath.Join(t.TempDir(), "projects.json")
	if err := Add(file, "blog", "/a"); err != nil {
		t.Fatal(err)
	}
	if removed, err := Remove(file, "/a"); err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if removed, err := Remove(file, "/a"); err != nil || removed {
		t.Fatalf("second removal: removed=%v err=%v", removed, err)
	}
}
