package main

// Commands with machine-readable output, used by the macOS app.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/cve"
	"github.com/ruimiguelcosta/stacknest/internal/laravel"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
	"github.com/ruimiguelcosta/stacknest/internal/services"
)

func printJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type phpVersion struct {
	Version string `json:"version"`
	Full    string `json:"full"`
}

func phpVersions(layout paths.Layout) []phpVersion {
	versions := []phpVersion{}
	for _, version := range installedPHP(layout) {
		versions = append(versions, phpVersion{Version: version, Full: fullPHPVersion(layout, version)})
	}
	return versions
}

// fullPHPVersion asks the binary itself, such as 8.4.27 for runtime 8.4.
func fullPHPVersion(layout paths.Layout, version string) string {
	output, err := exec.Command(layout.PHP(version), "-n", "-r", "echo PHP_VERSION;").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func laravelVersionsJSON(stdout io.Writer) error {
	releases, err := fetchReleases()
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, release := range releases {
		var compatible []string
		for _, candidate := range laravel.SupportedPHP {
			if _, err := laravel.ChoosePHP(release.PHPConstraint, []string{candidate}); err == nil {
				compatible = append(compatible, candidate)
			}
		}
		defaultPHP := ""
		if len(compatible) > 0 {
			defaultPHP = compatible[0]
		}
		out = append(out, map[string]any{
			"major":          release.Major,
			"version":        release.Version,
			"php_constraint": release.PHPConstraint,
			"default_php":    defaultPHP,
			"compatible_php": compatible,
		})
	}
	return printJSON(stdout, out)
}

func printStatus(layout paths.Layout, stdout io.Writer) error {
	data, err := os.ReadFile(layout.Status())
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("StackNest is not running (run: stacknest install)")
	}
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}

type listedProject struct {
	Name    string         `json:"name"`
	Dir     string         `json:"dir"`
	Domain  string         `json:"domain,omitempty"`
	Aliases []string       `json:"aliases,omitempty"`
	PHP     string         `json:"php,omitempty"`
	OPcache config.OPcache `json:"opcache"`
	Error   string         `json:"error,omitempty"`
}

func listJSON(layout paths.Layout, stdout io.Writer) error {
	projects, err := registry.Load(layout.Registry())
	if err != nil {
		return err
	}
	out := []listedProject{}
	for name, dir := range projects {
		item := listedProject{Name: name, Dir: dir}
		project, err := config.Load(dir)
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Domain, item.Aliases, item.PHP, item.OPcache = project.Domain, project.Aliases, project.PHP, project.OPcache
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return printJSON(stdout, out)
}

// set changes a linked project's settings; the daemon applies them within seconds.
func set(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("set", flag.ContinueOnError)
	flags.SetOutput(stderr)
	php := flags.String("php", "", "PHP version")
	domain := flags.String("domain", "", "main domain")
	aliases := flags.String("aliases", "", "comma-separated extra domains (empty clears them)")
	opcache := flags.Bool("opcache", true, "enable OPcache")
	memory := flags.Int("opcache-memory", 0, "OPcache memory in MB")
	jit := flags.Bool("jit", false, "enable the JIT")
	timestamps := flags.Bool("validate-timestamps", true, "check files for changes on every request")
	force := flags.Bool("force", false, "change PHP even if composer.lock requires another version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	dir, err := targetDir(flags.Args())
	if err != nil {
		return err
	}
	project, err := config.Load(dir)
	if err != nil {
		return err
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })

	if given["php"] && *php != project.PHP {
		if !layout.HasPHP(*php) {
			return fmt.Errorf("PHP %s is not installed", *php)
		}
		if !*force {
			if problems := platformProblems(layout, *php, dir); len(problems) > 0 {
				return fmt.Errorf("PHP %s does not meet this project's composer.lock requirements:\n  %s\n(use --force to change anyway)", *php, strings.Join(problems, "\n  "))
			}
		}
		project.PHP = *php
	}
	if given["domain"] {
		project.Domain = strings.ToLower(strings.TrimSpace(*domain))
	}
	if given["aliases"] {
		project.Aliases = nil
		for _, alias := range strings.Split(*aliases, ",") {
			if alias = strings.ToLower(strings.TrimSpace(alias)); alias != "" {
				project.Aliases = append(project.Aliases, alias)
			}
		}
	}
	if given["opcache"] {
		project.OPcache.Enabled = *opcache
	}
	if given["opcache-memory"] {
		project.OPcache.Memory = *memory
	}
	if given["jit"] {
		project.OPcache.JIT = *jit
	}
	if given["validate-timestamps"] {
		project.OPcache.ValidateTimestamps = *timestamps
	}
	if err := project.Validate(); err != nil {
		return err
	}
	if err := registry.Add(layout.Registry(), project.Name(), dir); err != nil {
		return err
	}
	if err := project.Write(dir); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "https://%s -> %s (PHP %s)\n", project.Domain, dir, project.PHP)
	return nil
}

// platformProblems asks Composer whether the locked packages accept a PHP version.
func platformProblems(layout paths.Layout, php, dir string) []string {
	if _, err := os.Stat(filepath.Join(dir, "composer.lock")); err != nil {
		return nil
	}
	if _, err := os.Stat(layout.Composer()); err != nil {
		return nil
	}
	cmd := exec.Command(layout.PHP(php), layout.Composer(), "check-platform-reqs", "--lock", "--format=json", "--no-interaction")
	cmd.Dir = dir
	output, _ := cmd.Output()
	var results []struct {
		Name              string `json:"name"`
		Status            string `json:"status"`
		FailedRequirement *struct {
			Source     string `json:"source"`
			Constraint string `json:"constraint"`
		} `json:"failed_requirement"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		return nil
	}
	var problems []string
	for _, r := range results {
		if r.Status == "success" {
			continue
		}
		problem := fmt.Sprintf("%s: %s", r.Name, r.Status)
		if r.FailedRequirement != nil {
			problem = fmt.Sprintf("%s %s is required by %s", r.Name, r.FailedRequirement.Constraint, r.FailedRequirement.Source)
		}
		problems = append(problems, problem)
	}
	return problems
}

var mariadbVersion = regexp.MustCompile(`Ver\s+(\d+\.\d+\.\d+)-MariaDB`)

func cves(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("cves", flag.ContinueOnError)
	flags.SetOutput(stderr)
	refresh := flags.Bool("refresh", false, "ignore the cached report")
	flags.Bool("json", true, "JSON output (always on)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	registered, err := registry.Load(layout.Registry())
	if err != nil {
		return err
	}
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)

	var projects []cve.Project
	phpUsers := map[string][]string{}
	var dbUsers []string
	for _, name := range names {
		dir := registered[name]
		project, err := config.Load(dir)
		if err != nil {
			continue
		}
		projects = append(projects, cve.Project{Name: name, Domain: project.Domain, Dir: dir})
		phpUsers[project.PHP] = append(phpUsers[project.PHP], name)
		if usesMySQL(dir) {
			dbUsers = append(dbUsers, name)
		}
	}

	var runtimes []cve.Runtime
	for _, version := range laravel.SupportedPHP {
		if users := phpUsers[version]; len(users) > 0 && layout.HasPHP(version) {
			if full := fullPHPVersion(layout, version); full != "" {
				runtimes = append(runtimes, cve.Runtime{Component: "PHP", Product: "php:php", Version: full, Projects: users})
			}
		}
	}
	if server, err := services.MariaDBServer(layout); err == nil {
		if output, err := exec.Command(server, "--version").Output(); err == nil {
			if match := mariadbVersion.FindSubmatch(output); match != nil {
				runtimes = append(runtimes, cve.Runtime{Component: "MariaDB", Product: "mariadb:mariadb", Version: string(match[1]), Projects: dbUsers})
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	scanner := cve.NewScanner(&http.Client{Timeout: 60 * time.Second})
	report, err := scanner.Cached(ctx, layout.Cache("cves.json"), projects, runtimes, *refresh)
	if err != nil {
		fmt.Fprintln(stderr, "warning:", err)
	}
	return printJSON(stdout, report)
}

// usesMySQL reports whether the project's .env points at a MySQL-compatible database.
func usesMySQL(dir string) bool {
	file, err := os.Open(filepath.Join(dir, ".env"))
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if ok && key == "DB_CONNECTION" {
			value = strings.Trim(value, `"'`)
			return value == "mysql" || value == "mariadb"
		}
	}
	return false
}
