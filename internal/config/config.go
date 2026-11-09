// Package config defines the per-project .stacknest.yaml file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const FileName = ".stacknest.yaml"

var domainPattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.test$`)

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
	Domain   string    `yaml:"domain,omitempty"`
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

// Name is the identifier derived from the domain.
func (p Project) Name() string {
	return strings.ReplaceAll(strings.TrimSuffix(p.Domain, ".test"), ".", "-")
}

func (p Project) Validate() error {
	if p.PHP == "" {
		return errors.New("php version is required")
	}
	if p.Domain != "" && !domainPattern.MatchString(p.Domain) {
		return fmt.Errorf("invalid domain %q (expected something like app.test)", p.Domain)
	}
	if p.OPcache.Memory <= 0 {
		return errors.New("opcache memory must be positive")
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
		return errors.New("database version is required")
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

// Load reads a project from dir; missing settings keep their defaults.
func Load(dir string) (Project, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return Project{}, err
	}
	project := Default("")
	if err := yaml.Unmarshal(data, &project); err != nil {
		return Project{}, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := project.Validate(); err != nil {
		return Project{}, fmt.Errorf("%s: %w", FileName, err)
	}
	return project, nil
}

// Find walks up from start and returns the nearest project and its directory.
func Find(start string) (Project, string, error) {
	dir := start
	for {
		project, err := Load(dir)
		if err == nil {
			return project, dir, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Project{}, "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Project{}, "", os.ErrNotExist
		}
		dir = parent
	}
}
