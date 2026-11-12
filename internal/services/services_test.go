package services

import (
	"os"
	"testing"

	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

func TestEnablePicksFreePortsAndRejectsClashes(t *testing.T) {
	settings := Settings{Mail: true}
	first, err := settings.Enable("mariadb", "11.8", 0, "")
	if err != nil || first.Port != 3306 {
		t.Fatalf("mariadb: %+v %v", first, err)
	}
	second, err := settings.Enable("mysql", "8.4", 0, "")
	if err != nil || second.Port != 3307 {
		t.Fatalf("mysql: %+v %v", second, err)
	}
	pg, _ := settings.Enable("postgres", "18", 0, "")
	if pg.Port != 5432 {
		t.Errorf("postgres port %d", pg.Port)
	}
	search, _ := settings.Enable("typesense", "30", 0, "")
	if search.APIKey == "" {
		t.Error("typesense needs an API key")
	}
	again, _ := settings.Enable("typesense", "30", 0, "")
	if again.APIKey != search.APIKey {
		t.Error("re-enabling changed the API key")
	}
	if _, err := settings.Enable("valkey", "9.1", 3306, ""); err == nil {
		t.Error("expected a port clash")
	}
	settings.Disable("valkey", "")
	if _, err := settings.Enable("mariadb", "11.8", 0, ""); err != nil || len(settings.Databases) != 3 {
		t.Errorf("re-enabling duplicated the instance: %+v %v", settings.Databases, err)
	}
	if !isPrimaryMySQL(settings, first) || isPrimaryMySQL(settings, second) {
		t.Error("the server on 3306 should own the default socket")
	}
}

func TestLoadAdoptsExistingDataAndSaves(t *testing.T) {
	layout := paths.Layout{Root: t.TempDir()}
	if err := os.MkdirAll(layout.Data("mariadb", "11.8"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(layout)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Mail || len(settings.Databases) != 1 || settings.Databases[0] != (Instance{Engine: "mariadb", Version: "11.8", Port: 3306}) {
		t.Fatalf("unexpected adoption: %+v", settings)
	}
	settings.Mail = false
	if err := Save(layout, settings); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := Load(layout); loaded.Mail {
		t.Error("saved settings were not read back")
	}
}

func TestAvailableExplainsMissingRuntimes(t *testing.T) {
	layout := paths.Layout{Root: t.TempDir()}
	_ = Save(layout, Settings{Databases: []Instance{{Engine: "postgres", Version: "18", Port: 5432}}})
	found, missing := Available(layout)
	if len(found) != 0 || len(missing) != 1 || missing[0].Name != "postgres-18" {
		t.Fatalf("found %+v missing %+v", found, missing)
	}
}
