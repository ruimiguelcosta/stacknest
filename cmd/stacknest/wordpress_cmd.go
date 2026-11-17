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
	"slices"
	"strings"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/database"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
	"github.com/ruimiguelcosta/stacknest/internal/services"
	"github.com/ruimiguelcosta/stacknest/internal/wordpress"
)

// wordpressPHP is preferred when installed: the newest series WordPress fully supports.
const wordpressPHP = "8.4"

// newWordPress creates NAME/ with the latest WordPress, a database, wp-config.php
// and a finished install, served at https://NAME.test.
func newWordPress(layout paths.Layout, name string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("new --wordpress", flag.ContinueOnError)
	flags.SetOutput(stderr)
	php := flags.String("php", "", "PHP version (default: "+wordpressPHP+" if installed, else the newest)")
	db := flags.String("db", "", "database server ENGINE[:VERSION], mariadb or mysql (default: the first one enabled)")
	locale := flags.String("locale", "pt_PT", "WordPress language, e.g. pt_PT, en_US")
	title := flags.String("title", name, "site title")
	email := flags.String("email", "admin@"+name+".test", "admin email")
	mail := flags.Bool("mail", true, "send wp_mail() to the local mailbox")
	dryRun := flags.Bool("dry-run", false, "show what would happen without creating anything")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	phpVersion := *php
	if phpVersion == "" {
		installed := installedPHP(layout)
		switch {
		case slices.Contains(installed, wordpressPHP):
			phpVersion = wordpressPHP
		case len(installed) > 0:
			phpVersion = installed[0]
		default:
			return errors.New("no PHP is installed (stacknest runtime install php)")
		}
	}

	settings, err := services.Load(layout)
	if err != nil {
		return err
	}
	var instance services.Instance
	if *db != "" {
		engine, version, _ := strings.Cut(*db, ":")
		if instance, err = pickInstance(settings, engine, version); err != nil {
			return err
		}
	} else {
		found := false
		for _, candidate := range settings.Databases {
			if candidate.Engine == "mariadb" || candidate.Engine == "mysql" {
				instance, found = candidate, true
				break
			}
		}
		if !found {
			return errors.New("WordPress needs MariaDB or MySQL (stacknest service enable mariadb)")
		}
	}
	if instance.Engine != "mariadb" && instance.Engine != "mysql" {
		return fmt.Errorf("WordPress does not support %s; use mariadb or mysql", instance.Engine)
	}

	project := config.Default(phpVersion)
	project.Domain = name + ".test"
	project.Mail = *mail
	project.Database = &config.Database{Engine: instance.Engine, Version: instance.Version}
	if err := project.Validate(); err != nil {
		return err
	}
	home := "https://" + project.Domain
	dbName := database.NameFor(project.Name())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Minute}
	offer, err := wordpress.Latest(ctx, client, *locale)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintf(stdout, "WordPress %s (%s) with PHP %s and %s database %q at %s\n", offer.Version, offer.Locale, phpVersion, services.Title(instance.Engine)+" "+instance.Version, dbName, home)
		return nil
	}
	if !layout.HasPHP(phpVersion) {
		return fmt.Errorf("PHP %s is not installed", phpVersion)
	}

	fmt.Fprintf(stdout, "Downloading WordPress %s (%s)\n", offer.Version, offer.Locale)
	if err := wordpress.Download(ctx, client, offer, name); err != nil {
		// Nothing existed at name before this command.
		_ = os.RemoveAll(name)
		return err
	}
	dir, err := filepath.Abs(name)
	if err != nil {
		return err
	}

	if err := database.Create(layout, instance, dbName); err != nil {
		return fmt.Errorf("WordPress downloaded to %s, but the database was not created: %w", dir, err)
	}
	env := database.EnvValues(instance, dbName)
	sample, err := os.ReadFile(filepath.Join(dir, "wp-config-sample.php"))
	if err != nil {
		return err
	}
	wpConfig, err := wordpress.Config(string(sample), wordpress.DB{
		Name: dbName, User: env["DB_USERNAME"], Password: env["DB_PASSWORD"], Host: env["DB_HOST"] + ":" + env["DB_PORT"],
	}, home)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte(wpConfig), 0o640); err != nil {
		return err
	}
	if *mail {
		plugins := filepath.Join(dir, "wp-content", "mu-plugins")
		if err := os.MkdirAll(plugins, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(plugins, "stacknest-mail.php"), []byte(wordpress.MailPlugin(services.MailSMTPPort)), 0o644); err != nil {
			return err
		}
	}

	password, err := wordpress.Password()
	if err != nil {
		return err
	}
	script, err := os.CreateTemp("", "stacknest-wp-install-*.php")
	if err != nil {
		return err
	}
	defer os.Remove(script.Name())
	if _, err := script.WriteString(wordpress.InstallScript); err != nil {
		return err
	}
	script.Close()
	install := exec.Command(layout.PHP(phpVersion), script.Name(), dir, *title, "admin", *email, password, home)
	install.Stderr = stderr
	if out, err := install.Output(); err != nil {
		return fmt.Errorf("WordPress install failed: %w: %s", err, out)
	}

	if err := project.Write(dir); err != nil {
		return err
	}
	if err := registry.Add(layout.Registry(), project.Name(), dir); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created %s (WordPress %s, PHP %s, database %s)\nAdmin: %s/wp-admin  user: admin  password: %s\n", home, offer.Version, phpVersion, dbName, home, password)
	return nil
}
