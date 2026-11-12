package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetReplacesEnablesAppendsAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	original := "APP_NAME=Laravel\nDB_CONNECTION=sqlite\n# DB_HOST=127.0.0.1\n# DB_PORT=3306\n\nMAIL_MAILER=log\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := t.TempDir()
	backup, err := Set(dir, backups, map[string]string{
		"DB_CONNECTION": "mariadb", "DB_HOST": "127.0.0.1", "DB_PORT": "3307", "DB_DATABASE": "shop", "APP_NAME": "My Shop",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".env"))
	want := "APP_NAME=\"My Shop\"\nDB_CONNECTION=mariadb\nDB_HOST=127.0.0.1\nDB_PORT=3307\n\nMAIL_MAILER=log\nDB_DATABASE=shop\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if saved, _ := os.ReadFile(backup); string(saved) != original {
		t.Error("backup does not hold the original file")
	}
	if info, _ := os.Stat(filepath.Join(dir, ".env")); info.Mode().Perm() != 0o600 {
		t.Error("file permissions changed")
	}
	if Get(dir, "APP_NAME") != "My Shop" {
		t.Errorf("Get = %q", Get(dir, "APP_NAME"))
	}
	if again, _ := Set(dir, backups, map[string]string{"DB_PORT": "3307"}); again != "" {
		t.Error("an unchanged file was backed up again")
	}
	second, err := Set(dir, backups, map[string]string{"DB_PORT": "3308"})
	if err != nil || second == backup {
		t.Fatalf("a second change in the same second reused the backup name: %s %v", second, err)
	}
	if saved, _ := os.ReadFile(backup); string(saved) != original {
		t.Error("the first backup was overwritten")
	}
}
