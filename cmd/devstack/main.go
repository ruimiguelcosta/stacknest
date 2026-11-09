// Command devstack manages per-project PHP development environments.
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
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/laravel"
)

const usage = `usage:
  devstack laravel versions
  devstack new <name> [--laravel MAJOR] [--php VERSION] [--db ENGINE:VERSION] [--redis] [--mail] [--dry-run]
`

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "devstack:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	switch {
	case len(args) == 2 && args[0] == "laravel" && args[1] == "versions":
		return listVersions(stdout)
	case len(args) >= 2 && args[0] == "new":
		return newProject(args[1], args[2:], stdout, stderr)
	}
	return errors.New("invalid command\n" + usage)
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

func newProject(name string, args []string, stdout, stderr io.Writer) error {
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

	composerArgs := laravel.CreateProjectArgs(release.Major, name)
	if *dryRun {
		fmt.Fprintf(stdout, "Laravel %d (%s) with PHP %s\ncomposer %s\n", release.Major, release.Version, phpVersion, strings.Join(composerArgs, " "))
		return nil
	}

	phpBin, err := runtimeBin(phpVersion)
	if err != nil {
		return err
	}
	composer := exec.Command("composer", composerArgs...)
	composer.Env = append(os.Environ(), "PATH="+phpBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	composer.Stdout, composer.Stderr = stdout, stderr
	if err := composer.Run(); err != nil {
		return fmt.Errorf("composer create-project: %w", err)
	}
	return project.Write(name)
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

// runtimeBin locates a PHP version installed by devstack.
func runtimeBin(version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(home, ".devstack", "runtimes", "php", version, "bin")
	if _, err := os.Stat(filepath.Join(bin, "php")); err != nil {
		return "", fmt.Errorf("PHP %s is not installed in %s", version, bin)
	}
	return bin, nil
}
