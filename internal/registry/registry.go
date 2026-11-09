// Package registry stores which project directories StackNest serves.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Load returns project name to absolute directory; a missing file is empty.
func Load(file string) (map[string]string, error) {
	projects := map[string]string{}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return projects, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return projects, nil
}

// Add registers a directory under name, replacing any older name it had.
func Add(file, name, dir string) error {
	projects, err := Load(file)
	if err != nil {
		return err
	}
	if current, taken := projects[name]; taken && current != dir {
		return fmt.Errorf("domain already used by %s", current)
	}
	for existing, path := range projects {
		if path == dir {
			delete(projects, existing)
		}
	}
	projects[name] = dir
	return save(file, projects)
}

// Remove stops serving the project in dir and reports whether it was registered.
func Remove(file, dir string) (bool, error) {
	projects, err := Load(file)
	if err != nil {
		return false, err
	}
	removed := false
	for name, path := range projects {
		if path == dir {
			delete(projects, name)
			removed = true
		}
	}
	if !removed {
		return false, nil
	}
	return true, save(file, projects)
}

func save(file string, projects map[string]string) error {
	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
