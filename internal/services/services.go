// Package services describes the shared background services StackNest runs:
// database servers, a Redis-compatible cache, a mail catcher and a search
// engine. Which ones run, in which versions and on which ports is kept in
// services.json; their programs come from StackNest's own runtimes.
package services

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

const (
	MailSMTPPort = 2525
	MailWebPort  = 8026
	MailDomain   = "mail.test"
	// mysqlSocketLink is where MariaDB and MySQL clients look by default.
	mysqlSocketLink = "/tmp/mysql.sock"
)

// Engines that can serve a project database.
var Engines = []string{"mariadb", "mysql", "postgres"}

var defaultPort = map[string]int{"mariadb": 3306, "mysql": 3306, "postgres": 5432, "valkey": 6379, "typesense": 8108}

var titles = map[string]string{
	"mariadb": "MariaDB", "mysql": "MySQL", "postgres": "PostgreSQL",
	"valkey": "Valkey", "mailpit": "Mailpit", "typesense": "Typesense",
}

// Title is the display name of an engine or service.
func Title(name string) string {
	if title, ok := titles[name]; ok {
		return title
	}
	return name
}

// Instance is one configured service.
type Instance struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Port    int    `json:"port"`
	// APIKey is used by Typesense.
	APIKey string `json:"api_key,omitempty"`
}

// ID names the instance in logs, status and the data folder, such as mariadb-11.8.
func (i Instance) ID() string { return i.Engine + "-" + i.Version }

type Settings struct {
	Databases []Instance `json:"databases"`
	Cache     *Instance  `json:"cache,omitempty"`
	Search    *Instance  `json:"search,omitempty"`
	Mail      bool       `json:"mail"`
}

// Find returns the database instance of an engine, the first one when version is empty.
func (s Settings) Find(engine, version string) (Instance, bool) {
	for _, db := range s.Databases {
		if db.Engine == engine && (version == "" || db.Version == version) {
			return db, true
		}
	}
	return Instance{}, false
}

func file(layout paths.Layout) string { return filepath.Join(layout.Root, "services.json") }

// Load reads services.json. Without one, it adopts the database data that
// already exists and turns on the mailbox, so older installs keep working.
func Load(layout paths.Layout) (Settings, error) {
	data, err := os.ReadFile(file(layout))
	if errors.Is(err, os.ErrNotExist) {
		return adopt(layout), nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("services.json: %w", err)
	}
	return settings, nil
}

func adopt(layout paths.Layout) Settings {
	settings := Settings{Mail: true}
	for _, engine := range Engines {
		versions, _ := os.ReadDir(layout.Data(engine))
		for _, v := range versions {
			if v.IsDir() {
				settings.Databases = append(settings.Databases, Instance{Engine: engine, Version: v.Name()})
			}
		}
	}
	for i := range settings.Databases {
		settings.Databases[i].Port = settings.nextPort(settings.Databases[i].Engine)
	}
	return settings
}

// Save validates and writes services.json.
func Save(layout paths.Layout, settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	sort.SliceStable(settings.Databases, func(i, j int) bool { return settings.Databases[i].ID() < settings.Databases[j].ID() })
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(layout.Root, 0o755); err != nil {
		return err
	}
	tmp := file(layout) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file(layout))
}

func (s Settings) Validate() error {
	used := map[int]string{}
	if s.Mail {
		used[MailSMTPPort], used[MailWebPort] = "mailpit", "mailpit"
	}
	all := append([]Instance{}, s.Databases...)
	if s.Cache != nil {
		all = append(all, *s.Cache)
	}
	if s.Search != nil {
		all = append(all, *s.Search)
	}
	seen := map[string]bool{}
	for _, instance := range all {
		if instance.Port <= 0 || instance.Port > 65535 {
			return fmt.Errorf("%s: invalid port %d", instance.ID(), instance.Port)
		}
		if other, taken := used[instance.Port]; taken {
			return fmt.Errorf("port %d is used by both %s and %s", instance.Port, other, instance.ID())
		}
		used[instance.Port] = instance.ID()
		if seen[instance.ID()] {
			return fmt.Errorf("%s is configured twice", instance.ID())
		}
		seen[instance.ID()] = true
	}
	return nil
}

// nextPort is the engine's usual port, or the next one free in the settings.
func (s Settings) nextPort(engine string) int {
	used := map[int]bool{MailSMTPPort: true, MailWebPort: true}
	for _, db := range s.Databases {
		used[db.Port] = true
	}
	if s.Cache != nil {
		used[s.Cache.Port] = true
	}
	if s.Search != nil {
		used[s.Search.Port] = true
	}
	port := defaultPort[engine]
	for used[port] {
		port++
	}
	return port
}

// Enable adds or updates a service; port 0 picks one.
func (s *Settings) Enable(engine, version string, port int, apiKey string) (Instance, error) {
	if port == 0 {
		port = s.nextPort(engine)
		if existing, ok := s.Find(engine, version); ok {
			port = existing.Port
		}
	}
	instance := Instance{Engine: engine, Version: version, Port: port}
	switch engine {
	case "mariadb", "mysql", "postgres":
		s.Disable(engine, version)
		s.Databases = append(s.Databases, instance)
	case "valkey":
		s.Cache = &instance
	case "typesense":
		if apiKey == "" && s.Search != nil {
			apiKey = s.Search.APIKey
		}
		if apiKey == "" {
			apiKey = randomKey()
		}
		instance.APIKey = apiKey
		s.Search = &instance
	case "mailpit":
		s.Mail = true
		return Instance{Engine: "mailpit", Port: MailSMTPPort}, nil
	default:
		return Instance{}, fmt.Errorf("unknown service %q", engine)
	}
	return instance, s.Validate()
}

// Disable removes a service; for databases an empty version removes every version.
func (s *Settings) Disable(engine, version string) {
	switch engine {
	case "valkey":
		s.Cache = nil
	case "typesense":
		s.Search = nil
	case "mailpit":
		s.Mail = false
	default:
		kept := s.Databases[:0]
		for _, db := range s.Databases {
			if db.Engine != engine || (version != "" && db.Version != version) {
				kept = append(kept, db)
			}
		}
		s.Databases = kept
	}
}

func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Service is a long-running process the daemon keeps alive.
type Service struct {
	Name        string
	Title       string
	Binary      string
	Args        []string
	Port        int
	StopSignal  os.Signal
	StopTimeout time.Duration
	// Prepare runs before every start, for example to initialise a data directory.
	Prepare func() error
	// WebDomain, when set, is served over HTTPS and proxied to WebUpstream.
	WebDomain   string
	WebUpstream string
}

// Missing explains why a configured service cannot run.
type Missing struct {
	Name, Title, Reason string
}

// Available returns the configured services whose runtimes are installed,
// and why the others cannot run.
func Available(layout paths.Layout) ([]Service, []Missing) {
	settings, err := Load(layout)
	if err != nil {
		return nil, []Missing{{Name: "services", Title: "Serviços", Reason: err.Error()}}
	}
	var instances []Instance
	instances = append(instances, settings.Databases...)
	if settings.Cache != nil {
		instances = append(instances, *settings.Cache)
	}
	if settings.Search != nil {
		instances = append(instances, *settings.Search)
	}
	if settings.Mail {
		instances = append(instances, Instance{Engine: "mailpit"})
	}

	var found []Service
	var missing []Missing
	for _, instance := range instances {
		service, err := build(layout, settings, instance)
		if err != nil {
			missing = append(missing, Missing{Name: service.Name, Title: service.Title, Reason: err.Error()})
			continue
		}
		found = append(found, service)
	}
	return found, missing
}

// Runtime returns the installed runtime folder of an engine version; for
// mailpit the version may be empty and the newest is used.
func Runtime(layout paths.Layout, engine, version string) (string, error) {
	if version == "" {
		series, _ := os.ReadDir(filepath.Join(layout.Root, "runtimes", engine))
		for i := len(series) - 1; i >= 0; i-- {
			if series[i].IsDir() && series[i].Name()[0] != '.' {
				version = series[i].Name()
				break
			}
		}
	}
	dir := layout.RuntimeDir(engine, version)
	if _, err := os.Stat(dir); err != nil || version == "" {
		label := Title(engine)
		if version != "" {
			label += " " + version
		}
		return "", fmt.Errorf("%s is not installed (stacknest runtime install %s %s)", label, engine, version)
	}
	return dir, nil
}

func build(layout paths.Layout, settings Settings, instance Instance) (Service, error) {
	service := Service{
		Name:        instance.ID(),
		Title:       Title(instance.Engine) + " " + instance.Version,
		Port:        instance.Port,
		StopSignal:  syscall.SIGTERM,
		StopTimeout: 30 * time.Second,
	}
	if instance.Engine == "mailpit" {
		service.Name, service.Title = "mailpit", "Mailpit"
	}
	base, err := Runtime(layout, instance.Engine, instance.Version)
	if err != nil {
		return service, err
	}
	bin := func(program string) string { return filepath.Join(base, "bin", program) }
	data := layout.Data(instance.Engine, instance.Version)
	socket := filepath.Join(layout.Run(), instance.ID()+".sock")
	logFile := filepath.Join(layout.Logs(), service.Name+".log")

	switch instance.Engine {
	case "mariadb":
		service.Binary = bin("mariadbd")
		service.StopTimeout = 2 * time.Minute
		service.Args = []string{
			"--no-defaults", "--basedir=" + base, "--datadir=" + data,
			"--plugin-dir=" + filepath.Join(base, "lib", "plugin"),
			"--bind-address=127.0.0.1", fmt.Sprintf("--port=%d", instance.Port),
			"--socket=" + socket, "--pid-file=" + filepath.Join(layout.Run(), instance.ID()+".pid"),
			"--log-error=" + logFile,
		}
		primary := isPrimaryMySQL(settings, instance)
		service.Prepare = func() error {
			if err := initMariaDB(base, data); err != nil {
				return err
			}
			if primary {
				return linkSocket(socket)
			}
			return nil
		}
	case "mysql":
		service.Binary = bin("mysqld")
		service.StopTimeout = 2 * time.Minute
		service.Args = []string{
			"--no-defaults", "--basedir=" + base, "--datadir=" + data,
			"--bind-address=127.0.0.1", fmt.Sprintf("--port=%d", instance.Port), "--mysqlx=OFF",
			"--socket=" + socket, "--pid-file=" + filepath.Join(layout.Run(), instance.ID()+".pid"),
			"--log-error=" + logFile,
		}
		primary := isPrimaryMySQL(settings, instance)
		service.Prepare = func() error {
			if err := initMySQL(base, data); err != nil {
				return err
			}
			if primary {
				return linkSocket(socket)
			}
			return nil
		}
	case "postgres":
		service.Binary = bin("postgres")
		// SIGINT is PostgreSQL's fast shutdown; SIGTERM would wait for every client.
		service.StopSignal = syscall.SIGINT
		service.StopTimeout = time.Minute
		service.Args = []string{"-D", data, "-p", fmt.Sprint(instance.Port), "-h", "127.0.0.1", "-k", layout.Run()}
		service.Prepare = func() error { return initPostgres(base, data) }
	case "valkey":
		service.Binary = bin("valkey-server")
		service.Args = []string{"--port", fmt.Sprint(instance.Port), "--bind", "127.0.0.1", "--dir", data, "--daemonize", "no"}
		service.Prepare = func() error { return os.MkdirAll(data, 0o700) }
	case "typesense":
		service.Binary = bin("typesense-server")
		service.Args = []string{
			"--data-dir", data, "--api-key", instance.APIKey,
			"--api-address", "127.0.0.1", "--api-port", fmt.Sprint(instance.Port),
			"--peering-address", "127.0.0.1", "--enable-cors",
		}
		service.Prepare = func() error { return os.MkdirAll(data, 0o700) }
	case "mailpit":
		service.Binary = bin("mailpit")
		service.Port = MailSMTPPort
		service.Args = []string{
			"--smtp", fmt.Sprintf("127.0.0.1:%d", MailSMTPPort),
			"--listen", fmt.Sprintf("127.0.0.1:%d", MailWebPort),
			"--database", layout.Data("mailpit", "mailpit.db"),
			"--smtp-auth-accept-any", "--smtp-auth-allow-insecure",
		}
		service.Prepare = func() error { return os.MkdirAll(layout.Data("mailpit"), 0o700) }
		service.WebDomain = MailDomain
		service.WebUpstream = fmt.Sprintf("127.0.0.1:%d", MailWebPort)
	}
	return service, nil
}

// isPrimaryMySQL picks the MySQL-family server that owns /tmp/mysql.sock:
// the one on 3306, otherwise the first configured.
func isPrimaryMySQL(settings Settings, instance Instance) bool {
	var first *Instance
	for i, db := range settings.Databases {
		if db.Engine != "mariadb" && db.Engine != "mysql" {
			continue
		}
		if db.Port == 3306 {
			return db.ID() == instance.ID()
		}
		if first == nil {
			first = &settings.Databases[i]
		}
	}
	return first != nil && first.ID() == instance.ID()
}

// initMariaDB creates an empty data directory with a passwordless root user,
// the usual local development setup. Existing data is left alone.
func initMariaDB(base, data string) error {
	if _, err := os.Stat(filepath.Join(data, "mysql")); err == nil {
		return nil
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	script := filepath.Join(base, "scripts", "mariadb-install-db")
	if _, err := os.Stat(script); err != nil {
		script = filepath.Join(base, "bin", "mariadb-install-db")
	}
	install := exec.Command(script, "--no-defaults", "--basedir="+base, "--datadir="+data,
		"--auth-root-authentication-method=normal", "--skip-test-db")
	if output, err := install.CombinedOutput(); err != nil {
		return fmt.Errorf("initialise mariadb data: %w: %s", err, output)
	}
	return nil
}

func initMySQL(base, data string) error {
	if _, err := os.Stat(filepath.Join(data, "mysql")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(data), 0o700); err != nil {
		return err
	}
	install := exec.Command(filepath.Join(base, "bin", "mysqld"), "--no-defaults", "--initialize-insecure",
		"--basedir="+base, "--datadir="+data)
	if output, err := install.CombinedOutput(); err != nil {
		return fmt.Errorf("initialise mysql data: %w: %s", err, output)
	}
	return nil
}

// initPostgres creates a cluster whose postgres user needs no password from this Mac.
func initPostgres(base, data string) error {
	if _, err := os.Stat(filepath.Join(data, "PG_VERSION")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(data), 0o700); err != nil {
		return err
	}
	install := exec.Command(filepath.Join(base, "bin", "initdb"), "-D", data, "-U", "postgres",
		"--auth=trust", "-E", "UTF8", "--locale=en_US.UTF-8")
	if output, err := install.CombinedOutput(); err != nil {
		return fmt.Errorf("initialise postgres data: %w: %s", err, output)
	}
	return nil
}

// linkSocket points the default client socket at ours, unless another server owns it.
func linkSocket(socket string) error {
	target, err := os.Readlink(mysqlSocketLink)
	switch {
	case err == nil && target == socket:
		return nil
	case err == nil:
		if _, statErr := os.Stat(target); statErr == nil {
			return nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil
	}
	_ = os.Remove(mysqlSocketLink)
	return os.Symlink(socket, mysqlSocketLink)
}

// PortFree reports whether nothing listens on the local TCP port.
func PortFree(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
