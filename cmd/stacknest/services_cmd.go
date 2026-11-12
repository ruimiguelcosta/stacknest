package main

// Runtimes, shared services, project databases and mail simulation.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/database"
	"github.com/ruimiguelcosta/stacknest/internal/dotenv"
	"github.com/ruimiguelcosta/stacknest/internal/inbound"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
	"github.com/ruimiguelcosta/stacknest/internal/runtime"
	"github.com/ruimiguelcosta/stacknest/internal/services"
)

var downloadClient = &http.Client{Timeout: 30 * time.Minute}

// runtimeCommand: catalog | list | install NAME [SERIES] | remove NAME SERIES
func runtimeCommand(layout paths.Layout, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: stacknest runtime catalog|list|install NAME [SERIES]|remove NAME SERIES")
	}
	switch {
	case args[0] == "catalog" && len(args) == 1:
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		catalog, err := runtime.FetchCatalog(ctx, downloadClient)
		if err != nil {
			return err
		}
		return printJSON(stdout, catalog)
	case args[0] == "list" && len(args) == 1:
		installed := runtime.List(layout)
		if installed == nil {
			installed = []runtime.Installed{}
		}
		return printJSON(stdout, installed)
	case args[0] == "install" && (len(args) == 2 || len(args) == 3):
		series := ""
		if len(args) == 3 {
			series = args[2]
		}
		entry, err := installRuntime(layout, args[1], series, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "installed %s %s %s\n", entry.Name, entry.Series, entry.Version)
		return nil
	case args[0] == "remove" && len(args) == 3:
		if users := runtimeUsers(layout, args[1], args[2]); len(users) > 0 {
			return fmt.Errorf("%s %s is still used by %s", services.Title(args[1]), args[2], strings.Join(users, ", "))
		}
		return runtime.Remove(layout, args[1], args[2])
	}
	return errors.New("usage: stacknest runtime catalog|list|install NAME [SERIES]|remove NAME SERIES")
}

// installRuntime downloads a catalog build, printing "progress N" lines the app reads.
func installRuntime(layout paths.Layout, name, series string, stdout io.Writer) (runtime.Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	catalog, err := runtime.FetchCatalog(ctx, downloadClient)
	if err != nil {
		return runtime.Entry{}, err
	}
	entry, err := catalog.Find(name, series)
	if err != nil {
		return runtime.Entry{}, err
	}
	fmt.Fprintf(stdout, "downloading %s %s (%d MB)\n", services.Title(name), entry.Version, entry.Size>>20)
	archive, err := runtime.Download(ctx, downloadClient, entry, func(percent int) {
		if percent%5 == 0 {
			fmt.Fprintf(stdout, "progress %d\n", percent)
		}
	})
	if err != nil {
		return runtime.Entry{}, err
	}
	defer os.Remove(archive)
	if err := runtime.Install(layout, entry.Name, entry.Series, entry.Version, archive); err != nil {
		return runtime.Entry{}, err
	}
	return entry, nil
}

// runtimeUsers names the projects and services that need a runtime.
func runtimeUsers(layout paths.Layout, name, series string) []string {
	var users []string
	if name == "php" {
		projects, _ := registry.Load(layout.Registry())
		for project, dir := range projects {
			if p, err := config.Load(dir); err == nil && p.PHP == series {
				users = append(users, project)
			}
		}
		return users
	}
	settings, err := services.Load(layout)
	if err != nil {
		return nil
	}
	for _, db := range settings.Databases {
		if db.Engine == name && db.Version == series {
			users = append(users, "the "+db.ID()+" service")
		}
	}
	for _, other := range []*services.Instance{settings.Cache, settings.Search} {
		if other != nil && other.Engine == name && other.Version == series {
			users = append(users, "the "+other.ID()+" service")
		}
	}
	return users
}

// serviceCommand: list | enable NAME [--version V] [--port N] [--api-key K] | disable NAME [--version V]
func serviceCommand(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		settings, err := services.Load(layout)
		if err != nil {
			return err
		}
		return printJSON(stdout, settings)
	}
	if len(args) < 2 || (args[0] != "enable" && args[0] != "disable") {
		return errors.New("usage: stacknest service list|enable NAME [--version V] [--port N]|disable NAME [--version V]")
	}
	name := args[1]
	flags := flag.NewFlagSet("service", flag.ContinueOnError)
	flags.SetOutput(stderr)
	version := flags.String("version", "", "version series, such as 11.8")
	port := flags.Int("port", 0, "TCP port (default: the usual one, or the next free)")
	apiKey := flags.String("api-key", "", "Typesense API key")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	settings, err := services.Load(layout)
	if err != nil {
		return err
	}
	if args[0] == "disable" {
		settings.Disable(name, *version)
		if err := services.Save(layout, settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s disabled; its data was kept in %s\n", services.Title(name), layout.Data(name))
		return nil
	}

	// Install the runtime first when it is missing, so enabling is one step.
	if name != "mailpit" && *version == "" {
		installed := runtime.List(layout)
		for _, r := range installed {
			if r.Name == name {
				*version = r.Series
				break
			}
		}
	}
	if _, err := services.Runtime(layout, name, *version); err != nil {
		entry, err := installRuntime(layout, name, *version, stdout)
		if err != nil {
			return err
		}
		*version = entry.Series
	}
	instance, err := settings.Enable(name, *version, *port, *apiKey)
	if err != nil {
		return err
	}
	if err := services.Save(layout, settings); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s enabled on port %d\n", services.Title(name)+" "+instance.Version, instance.Port)
	return nil
}

// dbCommand: create [--engine E] [--version V] [--name N] [--env] [DIR] | list [--engine E] [--version V]
func dbCommand(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "list") {
		return errors.New("usage: stacknest db create [--engine E] [--version V] [--name N] [--env] [DIR] | db list [--engine E] [--version V]")
	}
	flags := flag.NewFlagSet("db", flag.ContinueOnError)
	flags.SetOutput(stderr)
	engine := flags.String("engine", "", "mariadb, mysql or postgres")
	version := flags.String("version", "", "server version")
	name := flags.String("name", "", "database name (default: from the project name)")
	writeEnv := flags.Bool("env", false, "point the project's .env at the database")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	settings, err := services.Load(layout)
	if err != nil {
		return err
	}

	if args[0] == "list" {
		instance, err := pickInstance(settings, *engine, *version)
		if err != nil {
			return err
		}
		names, err := database.List(layout, instance)
		if err != nil {
			return err
		}
		if names == nil {
			names = []string{}
		}
		return printJSON(stdout, names)
	}

	dir, err := targetDir(flags.Args())
	if err != nil {
		return err
	}
	project, err := config.Load(dir)
	if err != nil {
		return err
	}
	if *engine == "" && project.Database != nil {
		*engine, *version = project.Database.Engine, project.Database.Version
	}
	instance, err := pickInstance(settings, *engine, *version)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = database.NameFor(project.Name())
	}
	if err := database.Create(layout, instance, *name); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "database %s ready on %s (127.0.0.1:%d)\n", *name, services.Title(instance.Engine)+" "+instance.Version, instance.Port)
	if !*writeEnv {
		return nil
	}
	values := database.EnvValues(instance, *name)
	if instance.Engine == "mariadb" && laravelMajor(dir) > 0 && laravelMajor(dir) < 11 {
		values["DB_CONNECTION"] = "mysql" // the mariadb driver arrived in Laravel 11
	}
	if err := writeDotenv(dir, values, stdout); err != nil {
		return err
	}
	project.Database = &config.Database{Engine: instance.Engine, Version: instance.Version}
	return project.Write(dir)
}

func pickInstance(settings services.Settings, engine, version string) (services.Instance, error) {
	if engine == "" {
		if len(settings.Databases) == 0 {
			return services.Instance{}, errors.New("no database server is enabled (stacknest service enable mariadb)")
		}
		return settings.Databases[0], nil
	}
	instance, ok := settings.Find(engine, version)
	if !ok {
		label := strings.TrimSpace(services.Title(engine) + " " + version)
		return services.Instance{}, fmt.Errorf("%s is not enabled (stacknest service enable %s)", label, engine)
	}
	return instance, nil
}

// laravelMajor reads the framework's major version from composer.lock, or 0.
func laravelMajor(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "composer.lock"))
	if err != nil {
		return 0
	}
	var lock struct {
		Packages []struct{ Name, Version string } `json:"packages"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return 0
	}
	for _, p := range lock.Packages {
		if p.Name == "laravel/framework" {
			major, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(p.Version, "v"), ".", 2)[0])
			return major
		}
	}
	return 0
}

func writeDotenv(dir string, values map[string]string, stdout io.Writer) error {
	backup, err := dotenv.Set(dir, values)
	if err != nil {
		return err
	}
	if backup != "" {
		fmt.Fprintf(stdout, ".env updated (previous version saved as %s)\n", filepath.Base(backup))
	}
	return nil
}

// envCommand: env [--mail] [--cache] [--search] [DIR] points .env at StackNest's services.
func envCommand(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("env", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mail := flags.Bool("mail", false, "send mail to the local mailbox")
	cache := flags.Bool("cache", false, "use the Valkey server for Redis")
	search := flags.Bool("search", false, "use the Typesense server for Laravel Scout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	dir, err := targetDir(flags.Args())
	if err != nil {
		return err
	}
	settings, err := services.Load(layout)
	if err != nil {
		return err
	}
	values := map[string]string{}
	if *mail {
		values["MAIL_MAILER"], values["MAIL_HOST"], values["MAIL_PORT"] = "smtp", "127.0.0.1", fmt.Sprint(services.MailSMTPPort)
		values["MAIL_USERNAME"], values["MAIL_PASSWORD"], values["MAIL_ENCRYPTION"] = "null", "null", "null"
	}
	if *cache {
		if settings.Cache == nil {
			return errors.New("Valkey is not enabled (stacknest service enable valkey)")
		}
		values["REDIS_HOST"], values["REDIS_PORT"], values["REDIS_PASSWORD"] = "127.0.0.1", fmt.Sprint(settings.Cache.Port), "null"
	}
	if *search {
		if settings.Search == nil {
			return errors.New("Typesense is not enabled (stacknest service enable typesense)")
		}
		values["SCOUT_DRIVER"] = "typesense"
		values["TYPESENSE_HOST"], values["TYPESENSE_PORT"], values["TYPESENSE_PROTOCOL"] = "127.0.0.1", fmt.Sprint(settings.Search.Port), "http"
		values["TYPESENSE_API_KEY"] = settings.Search.APIKey
	}
	if len(values) == 0 {
		return errors.New("choose at least one of --mail, --cache or --search")
	}
	return writeDotenv(dir, values, stdout)
}

// mailCommand: mail receive --url URL [--format raw|postmark|mailgun] --from F --to T --subject S --text T [--html H]
func mailCommand(layout paths.Layout, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "receive" {
		return errors.New("usage: stacknest mail receive --url URL [--format raw|postmark|mailgun] --from ADDRESS --to ADDRESS --subject TEXT --text TEXT [--html HTML]")
	}
	flags := flag.NewFlagSet("mail receive", flag.ContinueOnError)
	flags.SetOutput(stderr)
	target := flags.String("url", "", "the project's inbound mail webhook")
	format := flags.String("format", "raw", strings.Join(inbound.Formats, ", "))
	var message inbound.Message
	flags.StringVar(&message.From, "from", "cliente@example.test", "sender")
	flags.StringVar(&message.To, "to", "", "recipient")
	flags.StringVar(&message.Subject, "subject", "Teste", "subject")
	flags.StringVar(&message.Text, "text", "", "plain text body")
	flags.StringVar(&message.HTML, "html", "", "HTML body")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *target == "" {
		return errors.New("--url is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req, err := inbound.Request(ctx, *target, *format, message)
	if err != nil {
		return err
	}
	resp, err := inbound.Client(layout.RootCA()).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	fmt.Fprintf(stdout, "%s\n%s\n", resp.Status, strings.TrimSpace(string(body)))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("the project answered %s", resp.Status)
	}
	return nil
}
