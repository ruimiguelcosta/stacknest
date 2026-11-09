// Package fpm generates the PHP-FPM configuration of a project.
package fpm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

// Files holds the generated configuration paths.
type Files struct {
	Conf, INI, Log string
	// Fingerprint changes whenever the master process must restart.
	Fingerprint string
}

func INI(opcache config.OPcache) string {
	jit := "opcache.jit_buffer_size=0"
	if opcache.JIT {
		jit = "opcache.jit_buffer_size=64M\nopcache.jit=tracing"
	}
	return fmt.Sprintf(`memory_limit=512M
upload_max_filesize=128M
post_max_size=128M
max_execution_time=120
date.timezone=UTC
display_errors=On
error_reporting=E_ALL
opcache.enable=%d
opcache.enable_cli=0
opcache.memory_consumption=%d
opcache.validate_timestamps=%d
opcache.revalidate_freq=0
%s
`, boolToInt(opcache.Enabled), opcache.Memory, boolToInt(opcache.ValidateTimestamps), jit)
}

func conf(socket, errorLog string) string {
	return fmt.Sprintf(`[global]
error_log = %s
daemonize = no

[www]
listen = %s
listen.mode = 0600
pm = ondemand
pm.max_children = 8
pm.process_idle_timeout = 30s
catch_workers_output = yes
clear_env = no
`, errorLog, socket)
}

// Render writes the project's configuration files.
func Render(layout paths.Layout, name string, project config.Project) (Files, error) {
	dir := layout.Project(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Files{}, err
	}
	if err := os.MkdirAll(filepath.Dir(layout.Socket(name)), 0o755); err != nil {
		return Files{}, err
	}
	files := Files{
		Conf: filepath.Join(dir, "php-fpm.conf"),
		INI:  filepath.Join(dir, "php.ini"),
		Log:  filepath.Join(dir, "php-fpm.log"),
	}
	ini := INI(project.OPcache)
	fpmConf := conf(layout.Socket(name), files.Log)
	if err := os.WriteFile(files.INI, []byte(ini), 0o644); err != nil {
		return Files{}, err
	}
	if err := os.WriteFile(files.Conf, []byte(fpmConf), 0o644); err != nil {
		return Files{}, err
	}
	files.Fingerprint = project.PHP + "\x00" + ini + "\x00" + fpmConf
	return files, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
