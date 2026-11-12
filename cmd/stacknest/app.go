package main

// Commands with machine-readable output, used by the macOS app.

import (
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
	"sort"
	"strings"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/cve"
	"github.com/ruimiguelcosta/stacknest/internal/dotenv"
	"github.com/ruimiguelcosta/stacknest/internal/laravel"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
	"github.com/ruimiguelcosta/stacknest/internal/runtime"
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
	xdebug := flags.Bool("xdebug", false, "load Xdebug (sessions start on request)")
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
	if given["xdebug"] {
		if *xdebug {
			if _, err := os.Stat(filepath.Join(layout.Runtime(project.PHP), "lib", "xdebug.so")); err != nil {
				return fmt.Errorf("Xdebug is not available for PHP %s", project.PHP)
			}
		}
		project.Xdebug = *xdebug
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

	settings, _ := services.Load(layout)
	var projects []cve.Project
	phpUsers := map[string][]string{}
	dbUsers := map[string][]string{}
	for _, name := range names {
		dir := registered[name]
		project, err := config.Load(dir)
		if err != nil {
			continue
		}
		projects = append(projects, cve.Project{Name: name, Domain: project.Domain, Dir: dir})
		phpUsers[project.PHP] = append(phpUsers[project.PHP], name)
		if instance, ok := projectDatabase(settings, project, dir); ok {
			dbUsers[instance.ID()] = append(dbUsers[instance.ID()], name)
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
	products := map[string]string{"mariadb": "mariadb:mariadb", "mysql": "oracle:mysql", "postgres": "postgresql:postgresql"}
	installed := runtime.List(layout)
	for _, db := range settings.Databases {
		for _, r := range installed {
			if r.Name == db.Engine && r.Series == db.Version && r.Version != "" {
				runtimes = append(runtimes, cve.Runtime{Component: services.Title(db.Engine), Product: products[db.Engine], Version: r.Version, Projects: dbUsers[db.ID()]})
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

// projectDatabase finds the server a project uses: its .stacknest.yaml
// setting, or else the MySQL-compatible server on the port its .env names.
func projectDatabase(settings services.Settings, project config.Project, dir string) (services.Instance, bool) {
	if project.Database != nil {
		return settings.Find(project.Database.Engine, project.Database.Version)
	}
	connection := dotenv.Get(dir, "DB_CONNECTION")
	port := dotenv.Get(dir, "DB_PORT")
	for _, db := range settings.Databases {
		family := db.Engine == "postgres" && connection == "pgsql" ||
			db.Engine != "postgres" && (connection == "mysql" || connection == "mariadb")
		if family && (port == "" || port == fmt.Sprint(db.Port)) {
			return db, true
		}
	}
	return services.Instance{}, false
}
