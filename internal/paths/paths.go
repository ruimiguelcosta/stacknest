// Package paths defines where StackNest keeps its files.
package paths

import (
	"os"
	"path/filepath"
)

type Layout struct {
	Root string
}

// Default uses STACKNEST_HOME when set, otherwise ~/.stacknest.
func Default() (Layout, error) {
	if root := os.Getenv("STACKNEST_HOME"); root != "" {
		return Layout{Root: root}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	return Layout{Root: filepath.Join(home, ".stacknest")}, nil
}

func (l Layout) Runtime(php string) string {
	return filepath.Join(l.Root, "runtimes", "php", php)
}
func (l Layout) PHPBinDir(php string) string { return filepath.Join(l.Runtime(php), "bin") }
func (l Layout) PHP(php string) string       { return filepath.Join(l.PHPBinDir(php), "php") }
func (l Layout) PHPFPM(php string) string    { return filepath.Join(l.PHPBinDir(php), "php-fpm") }
func (l Layout) Composer() string            { return filepath.Join(l.Root, "tools", "composer.phar") }
func (l Layout) Shims() string               { return filepath.Join(l.Root, "bin") }
func (l Layout) Registry() string            { return filepath.Join(l.Root, "projects.json") }
func (l Layout) Project(name string) string  { return filepath.Join(l.Root, "projects", name) }
func (l Layout) Socket(name string) string   { return filepath.Join(l.Root, "run", name+".sock") }
func (l Layout) Caddy() string               { return filepath.Join(l.Root, "caddy") }
func (l Layout) RootCA() string {
	return filepath.Join(l.Caddy(), "pki", "authorities", "local", "root.crt")
}

// HasPHP reports whether a PHP version is installed.
func (l Layout) HasPHP(php string) bool {
	_, err := os.Stat(l.PHP(php))
	return err == nil
}
