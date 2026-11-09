// Package daemon keeps the PHP processes and web server in line with the registry.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/fpm"
	"github.com/ruimiguelcosta/stacknest/internal/localdns"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/proxy"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
)

const (
	reconcileInterval = 2 * time.Second
	stopTimeout       = 5 * time.Second
)

type Options struct {
	HTTPPort, HTTPSPort, DNSPort int
}

type process struct {
	cmd         *exec.Cmd
	done        chan struct{}
	fingerprint string
}

type Daemon struct {
	layout    paths.Layout
	options   Options
	processes map[string]*process
	caddyfile string
	problems  map[string]string
}

func New(layout paths.Layout, options Options) *Daemon {
	return &Daemon{
		layout:    layout,
		options:   options,
		processes: map[string]*process{},
		problems:  map[string]string{},
	}
}

// Run serves until ctx is cancelled, then stops every child process.
func (d *Daemon) Run(ctx context.Context) error {
	dns, err := localdns.Start(fmt.Sprintf("127.0.0.1:%d", d.options.DNSPort))
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	defer dns.Shutdown()
	defer d.stopAll()

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		if err := d.reconcile(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (d *Daemon) reconcile() error {
	projects, err := registry.Load(d.layout.Registry())
	if err != nil {
		d.report("registry", err)
		return nil
	}

	sites := make([]proxy.Site, 0, len(projects))
	active := map[string]bool{}
	for name, dir := range projects {
		project, err := d.ensure(name, dir)
		if err != nil {
			d.report(name, err)
			continue
		}
		d.report(name, nil)
		active[name] = true
		sites = append(sites, proxy.Site{Domain: project.Domain, Root: docRoot(dir), Socket: d.layout.Socket(name)})
	}
	for name := range d.processes {
		if !active[name] {
			d.stop(name)
		}
	}

	caddyfile := proxy.Caddyfile(proxy.Options{
		HTTPPort:  d.options.HTTPPort,
		HTTPSPort: d.options.HTTPSPort,
		Storage:   d.layout.Caddy(),
	}, sites)
	if caddyfile == d.caddyfile {
		return nil
	}
	if err := proxy.Apply(caddyfile); err != nil {
		return fmt.Errorf("web server: %w", err)
	}
	d.caddyfile = caddyfile
	return nil
}

// ensure starts the project's PHP-FPM, restarting it after a crash or a config change.
func (d *Daemon) ensure(name, dir string) (config.Project, error) {
	project, err := config.Load(dir)
	if err != nil {
		return config.Project{}, err
	}
	if project.Domain == "" {
		return config.Project{}, errors.New("no domain configured")
	}
	if !d.layout.HasPHP(project.PHP) {
		return config.Project{}, fmt.Errorf("PHP %s is not installed", project.PHP)
	}
	files, err := fpm.Render(d.layout, name, project)
	if err != nil {
		return config.Project{}, err
	}
	if current := d.processes[name]; current != nil {
		if running(current) && current.fingerprint == files.Fingerprint {
			return project, nil
		}
		d.stop(name)
	}

	cmd := exec.Command(d.layout.PHPFPM(project.PHP), "-c", files.INI, "-y", files.Conf)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return config.Project{}, fmt.Errorf("start php-fpm: %w", err)
	}
	started := &process{cmd: cmd, done: make(chan struct{}), fingerprint: files.Fingerprint}
	go func() {
		_ = cmd.Wait()
		close(started.done)
	}()
	d.processes[name] = started
	return project, nil
}

func running(p *process) bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (d *Daemon) stop(name string) {
	current := d.processes[name]
	delete(d.processes, name)
	if current == nil || !running(current) {
		return
	}
	_ = current.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-current.done:
	case <-time.After(stopTimeout):
		_ = current.cmd.Process.Kill()
		<-current.done
	}
}

func (d *Daemon) stopAll() {
	for name := range d.processes {
		d.stop(name)
	}
	if d.caddyfile != "" {
		_ = proxy.Stop()
	}
}

// report logs a problem once, until it changes or clears.
func (d *Daemon) report(key string, err error) {
	if err == nil {
		delete(d.problems, key)
		return
	}
	if d.problems[key] != err.Error() {
		d.problems[key] = err.Error()
		log.Printf("%s: %v", key, err)
	}
}

func docRoot(dir string) string {
	public := filepath.Join(dir, "public")
	if info, err := os.Stat(public); err == nil && info.IsDir() {
		return public
	}
	return dir
}
