// Package dotenv updates keys in a project's .env file without disturbing the rest.
package dotenv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Set writes the values into dir/.env. An existing key keeps its place, a
// commented-out key (as in Laravel's default "# DB_HOST=...") is enabled in
// place, and other keys are appended. The previous file is copied to
// .env.stacknest-backup-TIMESTAMP first. It returns the backup path, or ""
// when nothing changed.
func Set(dir string, values map[string]string) (string, error) {
	path := filepath.Join(dir, ".env")
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(original), "\n"), "\n")
	if len(original) == 0 {
		lines = nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		line := key + "=" + quote(values[key])
		active := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
		commented := regexp.MustCompile(`^\s*#\s*` + regexp.QuoteMeta(key) + `\s*=`)
		if i := indexOf(lines, active); i >= 0 {
			lines[i] = line
		} else if i := indexOf(lines, commented); i >= 0 {
			lines[i] = line
		} else {
			lines = append(lines, line)
		}
	}
	updated := strings.Join(lines, "\n") + "\n"
	if updated == string(original) {
		return "", nil
	}

	backup := ""
	if len(original) > 0 {
		backup = filepath.Join(dir, ".env.stacknest-backup-"+time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, original, 0o600); err != nil {
			return "", fmt.Errorf("back up .env: %w", err)
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return backup, os.WriteFile(path, []byte(updated), mode)
}

// Get returns a key's value from dir/.env, without quotes.
func Get(dir, key string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func indexOf(lines []string, pattern *regexp.Regexp) int {
	for i, line := range lines {
		if pattern.MatchString(line) {
			return i
		}
	}
	return -1
}

func quote(value string) string {
	if value == "" || !strings.ContainsAny(value, " #\"'$\\") {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
