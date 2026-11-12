// Package database creates project databases on StackNest's servers.
package database

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/services"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9_]{1,63}$`)

// NameFor turns a project name such as my-shop into my_shop.
func NameFor(project string) string {
	return strings.ReplaceAll(project, "-", "_")
}

// Create makes the database if it does not exist yet.
func Create(layout paths.Layout, instance services.Instance, name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid database name %q (letters, digits and underscores)", name)
	}
	base, err := services.Runtime(layout, instance.Engine, instance.Version)
	if err != nil {
		return err
	}
	port := fmt.Sprint(instance.Port)
	switch instance.Engine {
	case "mariadb", "mysql":
		client := filepath.Join(base, "bin", "mariadb")
		if instance.Engine == "mysql" {
			client = filepath.Join(base, "bin", "mysql")
		}
		sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", name)
		return run(client, "-uroot", "-h127.0.0.1", "-P"+port, "-e", sql)
	case "postgres":
		psql := filepath.Join(base, "bin", "psql")
		exists, err := output(psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-tAc",
			fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = '%s'", name))
		if err != nil {
			return err
		}
		if strings.TrimSpace(exists) == "1" {
			return nil
		}
		return run(psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-c", fmt.Sprintf(`CREATE DATABASE "%s"`, name))
	}
	return fmt.Errorf("unknown database engine %q", instance.Engine)
}

// List returns the user databases on a server.
func List(layout paths.Layout, instance services.Instance) ([]string, error) {
	base, err := services.Runtime(layout, instance.Engine, instance.Version)
	if err != nil {
		return nil, err
	}
	port := fmt.Sprint(instance.Port)
	var text string
	switch instance.Engine {
	case "mariadb", "mysql":
		client := filepath.Join(base, "bin", "mariadb")
		if instance.Engine == "mysql" {
			client = filepath.Join(base, "bin", "mysql")
		}
		text, err = output(client, "-uroot", "-h127.0.0.1", "-P"+port, "-N", "-e", "SHOW DATABASES")
	case "postgres":
		text, err = output(filepath.Join(base, "bin", "psql"), "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-tAc",
			"SELECT datname FROM pg_database WHERE NOT datistemplate")
	}
	if err != nil {
		return nil, err
	}
	system := map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true, "postgres": true}
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if name := strings.TrimSpace(line); name != "" && !system[name] {
			names = append(names, name)
		}
	}
	return names, nil
}

// EnvValues are the Laravel .env settings for a database on an instance.
func EnvValues(instance services.Instance, name string) map[string]string {
	values := map[string]string{
		"DB_HOST":     "127.0.0.1",
		"DB_PORT":     fmt.Sprint(instance.Port),
		"DB_DATABASE": name,
		"DB_PASSWORD": "",
	}
	switch instance.Engine {
	case "mariadb":
		values["DB_CONNECTION"], values["DB_USERNAME"] = "mariadb", "root"
	case "mysql":
		values["DB_CONNECTION"], values["DB_USERNAME"] = "mysql", "root"
	case "postgres":
		values["DB_CONNECTION"], values["DB_USERNAME"] = "pgsql", "postgres"
	}
	return values
}

func run(program string, args ...string) error {
	_, err := output(program, args...)
	return err
}

func output(program string, args ...string) (string, error) {
	cmd := exec.Command(program, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", filepath.Base(program), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
