// Package status is the snapshot the daemon publishes for the app and CLI.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
)

type Service struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Port    int    `json:"port,omitempty"`
	WebURL  string `json:"web_url,omitempty"`
	Log     string `json:"log,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Project struct {
	Name    string         `json:"name"`
	Dir     string         `json:"dir"`
	Domain  string         `json:"domain,omitempty"`
	Aliases []string       `json:"aliases,omitempty"`
	PHP     string         `json:"php,omitempty"`
	Root    string         `json:"root,omitempty"`
	Static  bool           `json:"static,omitempty"`
	Custom  bool           `json:"custom,omitempty"`
	Running bool           `json:"running"`
	OPcache config.OPcache `json:"opcache"`
	Log     string         `json:"log,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type Status struct {
	Updated   time.Time `json:"updated"`
	PID       int       `json:"pid"`
	HTTPSPort int       `json:"https_port"`
	Services  []Service `json:"services"`
	Projects  []Project `json:"projects"`
	// WebError is set when the web server configuration could not be loaded.
	WebError string `json:"web_error,omitempty"`
}

// Write replaces the file atomically, so readers never see half a snapshot.
func Write(file string, snapshot Status) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
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
