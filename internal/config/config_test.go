package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsUnknownEngine(t *testing.T) {
	p := Default("8.4")
	p.Database = &Database{Engine: "oracle", Version: "23"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected an error for an unknown engine")
	}
}

func TestWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	p := Default("8.4")
	p.Database = &Database{Engine: "postgres", Version: "17"}
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`php: "8.4"`, "engine: postgres", "validate_timestamps: true"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q in:\n%s", want, data)
		}
	}
}
