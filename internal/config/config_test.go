package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRejectsUnknownEngine(t *testing.T) {
	p := Default("8.4")
	p.Database = &Database{Engine: "oracle", Version: "23"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected an error for an unknown engine")
	}
}

func TestValidateRejectsNonTestDomain(t *testing.T) {
	p := Default("8.4")
	p.Domain = "app.com"
	if err := p.Validate(); err == nil {
		t.Fatal("expected an error for a non .test domain")
	}
}

func TestValidateChecksAliasesAndRoot(t *testing.T) {
	p := Default("8.4")
	p.Domain = "app.test"
	p.Aliases = []string{"www.app.test", "*.app.test"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Aliases = []string{"app.com"}
	if err := p.Validate(); err == nil {
		t.Error("expected an error for a non .test alias")
	}
	p.Aliases = nil
	p.Root = "../elsewhere"
	if err := p.Validate(); err == nil {
		t.Error("expected an error for a root outside the project")
	}
}

func TestWriteThenFindFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	p := Default("8.4")
	p.Domain = "shop.eu.test"
	p.Database = &Database{Engine: "postgres", Version: "17"}
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "app", "Models")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, root, err := Find(sub)
	if err != nil {
		t.Fatal(err)
	}
	if root != dir || got.PHP != "8.4" || got.Name() != "shop-eu" || !got.OPcache.ValidateTimestamps {
		t.Errorf("unexpected result: %s %+v", root, got)
	}
}

func TestLoadKeepsDefaultsForMissingSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("php: \"8.3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.OPcache.Memory != 256 || !got.OPcache.Enabled {
		t.Errorf("defaults lost: %+v", got.OPcache)
	}
}

func TestFindWithoutProject(t *testing.T) {
	if _, _, err := Find(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}
