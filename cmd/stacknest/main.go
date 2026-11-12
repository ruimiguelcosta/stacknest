// Command stacknest manages per-project PHP development environments.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/daemon"
	"github.com/ruimiguelcosta/stacknest/internal/herd"
	"github.com/ruimiguelcosta/stacknest/internal/laravel"
	"github.com/ruimiguelcosta/stacknest/internal/launchd"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
	"github.com/ruimiguelcosta/stacknest/internal/runtime"
)

const usage = `usage:
  stacknest install            install this binary, the php/composer commands and start at login
  stacknest uninstall          stop starting at login (projects and data are kept)
  stacknest import-herd [--dry-run]
  stacknest setup
  stacknest php install ARCHIVE
  stacknest php list
  stacknest laravel versions
  stacknest new NAME [--laravel MAJOR] [--php VERSION] [--db ENGINE:VERSION] [--redis] [--mail] [--allow-insecure] [--dry-run]
  stacknest link [--domain NAME.test] [--php VERSION] [DIR]
  stacknest unlink [DIR]
  stacknest list
  stacknest serve [--http-port N] [--https-port N] [--dns-port N]
`

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func main() {
	var err error
	switch filepath.Base(os.Args[0]) {
	case "php", "composer":
		err = runShim(filepath.Base(os.Args[0]), os.Args[1:])
	default:
		err = run(os.Args[1:], os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stacknest:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	layout, err := paths.Default()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 1 && args[0] == "install":
		return install(layout, stdout)
	case len(args) == 1 && args[0] == "uninstall":
		if err := launchd.Uninstall(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "StackNest stopped and will no longer start at login. Projects and data were kept.")
		return nil
	case len(args) >= 1 && args[0] == "import-herd":
		return importHerd(layout, args[1:], stdout, stderr)
	case len(args) == 1 && args[0] == "setup":
		return setup(layout, stdout)
	case len(args) == 3 && args[0] == "php" && args[1] == "install":
		version, err := runtime.InstallPHP(layout, args[2])
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "PHP %s installed\n", version)
		return nil
	case len(args) == 2 && args[0] == "php" && args[1] == "list":
		for _, version := range installedPHP(layout) {
			fmt.Fprintln(stdout, version)
		}
		return nil
	case len(args) == 2 && args[0] == "laravel" && args[1] == "versions":
		return listVersions(stdout)
	case len(args) >= 2 && args[0] == "new":
		return newProject(layout, args[1], args[2:], stdout, stderr)
	case len(args) >= 1 && args[0] == "link":
		return link(layout, args[1:], stdout, stderr)
	case len(args) >= 1 && args[0] == "unlink" && len(args) <= 2:
		return unlink(layout, args[1:], stdout)
	case len(args) == 1 && args[0] == "list":
		return list(layout, stdout)
	case len(args) >= 1 && args[0] == "serve":
		return serve(layout, args[1:], stderr)
	}
	return errors.New("invalid command\n" + usage)
}

func setup(layout paths.Layout, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runtime.InstallComposer(ctx, http.DefaultClient, layout); err != nil {
		return fmt.Errorf("install composer: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err := linkShims(layout, executable); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Composer installed. Add this to your PATH:\n  %s\n", layout.Shims())
	return nil
}

func linkShims(layout paths.Layout, executable string) error {
	if err := os.MkdirAll(layout.Shims(), 0o755); err != nil {
		return err
	}
	for _, name := range []string{"php", "composer"} {
		shim := filepath.Join(layout.Shims(), name)
		if err := os.Remove(shim); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Symlink(executable, shim); err != nil {
			return err
		}
	}
	return nil
}

// install copies this binary into StackNest's home and starts it at login.
func install(layout paths.Layout, stdout io.Writer) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	if source, err = filepath.EvalSymlinks(source); err != nil {
		return err
	}
	target := layout.Executable()
	if source != target {
		if err := copyExecutable(source, target); err != nil {
			return err
		}
	}
	if err := linkShims(layout, target); err != nil {
		return err
	}
	if _, err := os.Stat(layout.Composer()); errors.Is(err, os.ErrNotExist) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := runtime.InstallComposer(ctx, http.DefaultClient, layout); err != nil {
			return fmt.Errorf("install composer: %w", err)
		}
	}
	if err := os.MkdirAll(layout.Logs(), 0o755); err != nil {
		return err
	}
	plist := launchd.Plist(target, layout.Root, filepath.Join(layout.Logs(), "stacknest.log"))
	if err := launchd.Install(plist); err != nil {
		return err
	}
	fmt.Fprintf(stdout, `StackNest is running and will start at login.

Add this line to ~/.zshrc so php, composer and stacknest come from StackNest:
  export PATH="%s:$PATH"

Run these once (they ask for your password):
  sudo mkdir -p /etc/resolver
  sudo sh -c 'printf "nameserver 127.0.0.1\nport 5354\n" > /etc/resolver/test'
  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %q
`, layout.Shims(), layout.RootCA())
	return nil
}

// copyExecutable replaces target atomically, so a running daemon keeps its old file.
func copyExecutable(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func importHerd(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("import-herd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dryRun := flags.Bool("dry-run", false, "show what would be imported")
	if err := flags.Parse(args); err != nil {
		return err
	}
	root, err := herd.DefaultRoot()
	if err != nil {
		return err
	}
	sites, skipped, err := herd.Sites(root)
	if err != nil {
		return err
	}
	for _, reason := range skipped {
		fmt.Fprintf(stderr, "skipped %s\n", reason)
	}
	installed := installedPHP(layout)
	if len(installed) == 0 {
		return errors.New("no PHP version is installed")
	}

	failed := 0
	for _, site := range sites {
		php := site.PHP
		if !layout.HasPHP(php) {
			fmt.Fprintf(stderr, "%s: PHP %q is not available, using %s\n", site.Domains[0], php, installed[0])
			php = installed[0]
		}
		project, err := config.Load(site.Dir)
		existing := err == nil
		switch {
		case existing:
		case errors.Is(err, os.ErrNotExist):
			project = config.Default(php)
			project.Domain = site.Domains[0]
			project.Aliases = site.Domains[1:]
		default:
			fmt.Fprintf(stderr, "%s: %v\n", site.Dir, err)
			failed++
			continue
		}

		note := ""
		if existing {
			note = " (kept existing " + config.FileName + ")"
		}
		fmt.Fprintf(stdout, "https://%s -> %s (PHP %s)%s\n", strings.Join(project.Domains(), ", https://"), site.Dir, project.PHP, note)
		if site.CustomConfig != "" && project.Caddy == "" {
			fmt.Fprintf(stdout, "  note: Herd used custom nginx rules from %s; add equivalent caddy directives to %s if the site needs them\n", site.CustomConfig, config.FileName)
		}
		if *dryRun {
			continue
		}
		if !existing {
			if err := project.Write(site.Dir); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", site.Dir, err)
				failed++
				continue
			}
		}
		if err := registry.Add(layout.Registry(), project.Name(), site.Dir); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", site.Dir, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d sites were not imported", failed, len(sites))
	}
	return nil
}

func installedPHP(layout paths.Layout) []string {
	var installed []string
	for _, version := range laravel.SupportedPHP {
		if layout.HasPHP(version) {
			installed = append(installed, version)
		}
	}
	return installed
}

// runShim replaces this process with the PHP version the current project uses.
func runShim(name string, args []string) error {
	layout, err := paths.Default()
	if err != nil {
		return err
	}
	php, err := shimPHP(layout)
	if err != nil {
		return err
	}
	// PHP derives PHP_BINARY from argv[0], so it must be the full path.
	argv := []string{layout.PHP(php)}
	if name == "composer" {
		argv = append(argv, layout.Composer())
	}
	return syscall.Exec(layout.PHP(php), append(argv, args...), os.Environ())
}

func shimPHP(layout paths.Layout) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	project, _, err := config.Find(cwd)
	if err == nil {
		if !layout.HasPHP(project.PHP) {
			return "", fmt.Errorf("PHP %s is not installed", project.PHP)
		}
		return project.PHP, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	installed := installedPHP(layout)
	if len(installed) == 0 {
		return "", errors.New("no PHP version is installed")
	}
	return installed[0], nil
}

func fetchReleases() ([]laravel.Release, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return laravel.Fetch(ctx, http.DefaultClient)
}

func listVersions(stdout io.Writer) error {
	releases, err := fetchReleases()
	if err != nil {
		return err
	}
	for _, release := range releases {
		php, err := laravel.ChoosePHP(release.PHPConstraint, laravel.SupportedPHP)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Laravel %d\t%s\tPHP %s\t(default PHP %s)\n", release.Major, release.Version, release.PHPConstraint, php)
	}
	return nil
}

func newProject(layout paths.Layout, name string, args []string, stdout, stderr io.Writer) error {
	if !projectName.MatchString(name) {
		return fmt.Errorf("invalid project name %q (use lowercase letters, digits and hyphens)", name)
	}

	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	major := flags.Int("laravel", 0, "Laravel major version (default: latest)")
	php := flags.String("php", "", "PHP version (default: newest compatible)")
	db := flags.String("db", "", "database as ENGINE:VERSION")
	redis := flags.Bool("redis", false, "enable Redis")
	mail := flags.Bool("mail", false, "enable the local mailbox")
	allowInsecure := flags.Bool("allow-insecure", false, "install even if Composer blocks the release over security advisories")
	dryRun := flags.Bool("dry-run", false, "show what would happen without creating anything")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	releases, err := fetchReleases()
	if err != nil {
		return err
	}
	release, err := pickRelease(releases, *major)
	if err != nil {
		return err
	}

	candidates := laravel.SupportedPHP
	if *php != "" {
		candidates = []string{*php}
	}
	phpVersion, err := laravel.ChoosePHP(release.PHPConstraint, candidates)
	if err != nil {
		return fmt.Errorf("Laravel %d: %w", release.Major, err)
	}

	project := config.Default(phpVersion)
	project.Domain = name + ".test"
	project.Redis = *redis
	project.Mail = *mail
	if *db != "" {
		engine, version, ok := strings.Cut(*db, ":")
		if !ok {
			return fmt.Errorf("invalid --db %q (expected ENGINE:VERSION)", *db)
		}
		project.Database = &config.Database{Engine: engine, Version: version}
	}
	if err := project.Validate(); err != nil {
		return err
	}

	composerArgs := laravel.CreateProjectArgs(release.Major, name, *allowInsecure)
	if *dryRun {
		fmt.Fprintf(stdout, "Laravel %d (%s) with PHP %s at https://%s\ncomposer %s\n", release.Major, release.Version, phpVersion, project.Domain, strings.Join(composerArgs, " "))
		return nil
	}

	if !layout.HasPHP(phpVersion) {
		return fmt.Errorf("PHP %s is not installed", phpVersion)
	}
	if _, err := os.Stat(layout.Composer()); err != nil {
		return errors.New("composer is not installed (run: stacknest setup)")
	}
	composer := exec.Command(layout.PHP(phpVersion), append([]string{layout.Composer()}, composerArgs...)...)
	composer.Env = append(os.Environ(), "PATH="+layout.PHPBinDir(phpVersion)+string(os.PathListSeparator)+os.Getenv("PATH"))
	composer.Stdout, composer.Stderr = stdout, stderr
	if err := composer.Run(); err != nil {
		// The directory did not exist before this command, so it is safe to remove.
		if cleanupErr := os.RemoveAll(name); cleanupErr != nil {
			return fmt.Errorf("composer create-project: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return fmt.Errorf("composer create-project: %w (if Composer blocked it over security advisories, review them and use --allow-insecure)", err)
	}
	if *allowInsecure {
		fmt.Fprintln(stderr, "warning: installed without Composer's security blocking; this project may have known vulnerabilities")
	}
	if err := project.Write(name); err != nil {
		return err
	}
	dir, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if err := registry.Add(layout.Registry(), project.Name(), dir); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created https://%s\n", project.Domain)
	return nil
}

func pickRelease(releases []laravel.Release, major int) (laravel.Release, error) {
	if major == 0 {
		return releases[0], nil
	}
	offered := make([]string, 0, len(releases))
	for _, release := range releases {
		if release.Major == major {
			return release, nil
		}
		offered = append(offered, fmt.Sprint(release.Major))
	}
	return laravel.Release{}, fmt.Errorf("Laravel %d is not offered (choose %s)", major, strings.Join(offered, ", "))
}

func link(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("link", flag.ContinueOnError)
	flags.SetOutput(stderr)
	domain := flags.String("domain", "", "domain ending in .test")
	php := flags.String("php", "", "PHP version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	dir, err := targetDir(flags.Args())
	if err != nil {
		return err
	}

	project, err := config.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		if *php == "" {
			return fmt.Errorf("%s has no %s yet; pass --php", dir, config.FileName)
		}
		project = config.Default(*php)
		project.Domain = strings.ToLower(filepath.Base(dir)) + ".test"
	} else if err != nil {
		return err
	}
	if *php != "" {
		project.PHP = *php
	}
	if *domain != "" {
		project.Domain = *domain
	}
	if project.Domain == "" {
		return errors.New("a domain is required; pass --domain")
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

func unlink(layout paths.Layout, args []string, stdout io.Writer) error {
	dir, err := targetDir(args)
	if err != nil {
		return err
	}
	removed, err := registry.Remove(layout.Registry(), dir)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("%s is not linked", dir)
	}
	fmt.Fprintf(stdout, "Unlinked %s\n", dir)
	return nil
}

func targetDir(args []string) (string, error) {
	switch len(args) {
	case 0:
		return os.Getwd()
	case 1:
		return filepath.Abs(args[0])
	}
	return "", errors.New("expected a single directory")
}

func list(layout paths.Layout, stdout io.Writer) error {
	projects, err := registry.Load(layout.Registry())
	if err != nil {
		return err
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		project, err := config.Load(projects[name])
		if err != nil {
			fmt.Fprintf(stdout, "%s\terror: %v\n", projects[name], err)
			continue
		}
		fmt.Fprintf(stdout, "https://%s\tPHP %s\t%s\n", project.Domain, project.PHP, projects[name])
	}
	return nil
}

func serve(layout paths.Layout, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	httpPort := flags.Int("http-port", 80, "HTTP port")
	httpsPort := flags.Int("https-port", 443, "HTTPS port")
	dnsPort := flags.Int("dns-port", 5354, "DNS port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return daemon.New(layout, daemon.Options{HTTPPort: *httpPort, HTTPSPort: *httpsPort, DNSPort: *dnsPort}).Run(ctx)
}
