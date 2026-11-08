// Package config defines the per-project .devstack.yaml file.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const FileName = ".devstack.yaml"

type Database struct {
	Engine  string `yaml:"engine"`
	Version string `yaml:"version"`
}

type OPcache struct {
	Enabled            bool `yaml:"enabled"`
	Memory             int  `yaml:"memory"`
	ValidateTimestamps bool `yaml:"validate_timestamps"`
	JIT                bool `yaml:"jit"`
}

type Project struct {
	PHP      string    `yaml:"php"`
	Database *Database `yaml:"database,omitempty"`
	Redis    bool      `yaml:"redis"`
	OPcache  OPcache   `yaml:"opcache"`
	Mail     bool      `yaml:"mail"`
}

// Default returns development-friendly settings: OPcache stays on but keeps
// checking timestamps, so code edits show up without a manual reset.
func Default(php string) Project {
	return Project{
		PHP:     php,
		OPcache: OPcache{Enabled: true, Memory: 256, ValidateTimestamps: true},
	}
}

func (p Project) Validate() error {
	if p.PHP == "" {
		return fmt.Errorf("php version is required")
	}
	if p.OPcache.Memory <= 0 {
		return fmt.Errorf("opcache memory must be positive")
	}
	if p.Database == nil {
		return nil
	}
	switch p.Database.Engine {
	case "mariadb", "mysql", "postgres":
	default:
		return fmt.Errorf("unknown database engine %q (use mariadb, mysql or postgres)", p.Database.Engine)
	}
	if p.Database.Version == "" {
		return fmt.Errorf("database version is required")
	}
	return nil
}

// Write validates the project and saves it in dir.
func (p Project) Write(dir string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), data, 0o644)
}
