// Package services describes the shared background services StackNest runs:
// a database server and a local mail catcher.
package services

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

const (
	MariaDBVersion = "11.8"
	MariaDBPort    = 3306
	// MailSMTPPort matches the port Laravel projects commonly use for a local mailbox.
	MailSMTPPort = 2525
	MailWebPort  = 8026
	MailDomain   = "mail.test"
	// mysqlSocketLink is where MariaDB and MySQL clients look by default.
	mysqlSocketLink = "/tmp/mysql.sock"
)

// Service is a long-running process the daemon keeps alive.
type Service struct {
	Name        string
	Binary      string
	Args        []string
	Port        int
	StopTimeout time.Duration
	// Prepare runs before every start, for example to initialise a data directory.
	Prepare func() error
	// WebDomain, when set, is served over HTTPS and proxied to WebUpstream.
	WebDomain   string
	WebUpstream string
}

// Missing explains why a known service cannot run.
type Missing struct {
	Name, Reason string
}

// Available returns the services whose programs are installed, and why the others are not.
func Available(layout paths.Layout) ([]Service, []Missing) {
	var found []Service
	var missing []Missing
	for _, build := range []func(paths.Layout) (Service, error){mariaDB, mailpit} {
		service, err := build(layout)
		if err != nil {
			missing = append(missing, Missing{Name: service.Name, Reason: err.Error()})
			continue
		}
		found = append(found, service)
	}
	return found, missing
}

func mariaDB(layout paths.Layout) (Service, error) {
	service := Service{Name: "mariadb", Port: MariaDBPort, StopTimeout: 2 * time.Minute}
	server, err := find(layout, "mariadb", MariaDBVersion, "mariadb@"+MariaDBVersion, "mariadbd")
	if err != nil {
		return service, err
	}
	base := filepath.Dir(filepath.Dir(server))
	data := layout.Data("mariadb", MariaDBVersion)
	socket := filepath.Join(layout.Run(), "mariadb.sock")
	service.Binary = server
	service.Args = []string{
		"--no-defaults",
		"--basedir=" + base,
		"--datadir=" + data,
		"--plugin-dir=" + filepath.Join(base, "lib", "plugin"),
		"--bind-address=127.0.0.1",
		fmt.Sprintf("--port=%d", MariaDBPort),
		"--socket=" + socket,
		"--pid-file=" + filepath.Join(layout.Run(), "mariadb.pid"),
		"--log-error=" + filepath.Join(layout.Logs(), "mariadb.log"),
	}
	service.Prepare = func() error {
		if err := initMariaDB(base, data); err != nil {
			return err
		}
		return linkSocket(socket)
	}
	return service, nil
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
	install := exec.Command(filepath.Join(base, "bin", "mariadb-install-db"),
		"--no-defaults", "--basedir="+base, "--datadir="+data,
		"--auth-root-authentication-method=normal", "--skip-test-db")
	if output, err := install.CombinedOutput(); err != nil {
		return fmt.Errorf("initialise mariadb data: %w: %s", err, output)
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

func mailpit(layout paths.Layout) (Service, error) {
	service := Service{Name: "mailpit", Port: MailSMTPPort, StopTimeout: 10 * time.Second}
	binary, err := find(layout, "mailpit", "", "mailpit", "mailpit")
	if err != nil {
		return service, err
	}
	service.Binary = binary
	service.Args = []string{
		"--smtp", fmt.Sprintf("127.0.0.1:%d", MailSMTPPort),
		"--listen", fmt.Sprintf("127.0.0.1:%d", MailWebPort),
		"--database", layout.Data("mailpit", "mailpit.db"),
		"--smtp-auth-accept-any",
		"--smtp-auth-allow-insecure",
	}
	service.Prepare = func() error { return os.MkdirAll(layout.Data("mailpit"), 0o700) }
	service.WebDomain = MailDomain
	service.WebUpstream = fmt.Sprintf("127.0.0.1:%d", MailWebPort)
	return service, nil
}

// find looks for a program in StackNest's own runtimes, then in Homebrew.
func find(layout paths.Layout, name, version, formula, program string) (string, error) {
	candidates := []string{
		filepath.Join(layout.Root, "runtimes", name, version, "bin", program),
		filepath.Join("/opt/homebrew/opt", formula, "bin", program),
		filepath.Join("/usr/local/opt", formula, "bin", program),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s is not installed (brew install %s)", program, formula)
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
