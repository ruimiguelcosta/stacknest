// Package daemon keeps the PHP processes, shared services and web server in
// line with the registry.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/fpm"
	"github.com/ruimiguelcosta/stacknest/internal/localdns"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
	"github.com/ruimiguelcosta/stacknest/internal/proxy"
	"github.com/ruimiguelcosta/stacknest/internal/registry"
	"github.com/ruimiguelcosta/stacknest/internal/services"
	"github.com/ruimiguelcosta/stacknest/internal/status"
)

const (
	reconcileInterval = 2 * time.Second
	stopTimeout       = 5 * time.Second
	// A service that dies sooner than this after starting waits retryDelay before the next try.
	minUptime  = 10 * time.Second
	retryDelay = 30 * time.Second
)

type Options struct {
	HTTPPort, HTTPSPort, DNSPort int
}

type process struct {
	cmd         *exec.Cmd
	done        chan struct{}
	fingerprint string
	started     time.Time
	stopTimeout time.Duration
}

type Daemon struct {
	layout    paths.Layout
	options   Options
	processes map[string]*process
	services  map[string]*process
	retry     map[string]time.Time
	caddyfile string
	problems  map[string]string
}

func New(layout paths.Layout, options Options) *Daemon {
	return &Daemon{
		layout:    layout,
		options:   options,
		processes: map[string]*process{},
		services:  map[string]*process{},
		retry:     map[string]time.Time{},
		problems:  map[string]string{},
	}
}

// Run serves until ctx is cancelled, then stops every child process.
func (d *Daemon) Run(ctx context.Context) error {
	for _, dir := range []string{d.layout.Run(), d.layout.Logs()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	dns, err := localdns.Start(fmt.Sprintf("127.0.0.1:%d", d.options.DNSPort))
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	defer dns.Shutdown()
	defer d.stopAll()

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		d.reconcile()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (d *Daemon) reconcile() {
	snapshot := status.Status{Updated: time.Now(), PID: os.Getpid(), HTTPSPort: d.options.HTTPSPort}
	sites, serviceStates := d.ensureServices()
	snapshot.Services = serviceStates

	projects, err := registry.Load(d.layout.Registry())
	d.report("registry", err)
	active := map[string]bool{}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	siteIndex := map[string]int{}
	for _, name := range names {
		dir := projects[name]
		state := status.Project{Name: name, Dir: dir, Log: d.layout.FPMLog(name)}
		project, err := d.ensure(name, dir)
		d.report(name, err)
		if err != nil {
			state.Error = err.Error()
			if loaded, loadErr := config.Load(dir); loadErr == nil {
				fillProject(&state, loaded)
			}
			snapshot.Projects = append(snapshot.Projects, state)
			continue
		}
		active[name] = true
		root := docRoot(dir, project.Root)
		fillProject(&state, project)
		state.Root = root
		state.Static = project.Caddy == "" && isStatic(root)
		state.Running = true
		siteIndex[project.Domain] = len(snapshot.Projects)
		snapshot.Projects = append(snapshot.Projects, state)
		sites = append(sites, proxy.Site{
			Domains: project.Domains(),
			Root:    root,
			Socket:  d.layout.Socket(name),
			Custom:  project.Caddy,
			Static:  state.Static,
		})
	}
	for name := range d.processes {
		if !active[name] {
			d.stop(d.processes, name)
		}
	}
	for domain, err := range d.applyWeb(sites) {
		if i, ok := siteIndex[domain]; ok {
			snapshot.Projects[i].Running = false
			snapshot.Projects[i].Error = err
		}
	}
	snapshot.WebError = d.problems["web server"]
	d.report("status", status.Write(d.layout.Status(), snapshot))
}

func fillProject(state *status.Project, project config.Project) {
	state.Domain = project.Domain
	state.Aliases = project.Aliases
	state.PHP = project.PHP
	state.Custom = project.Caddy != ""
	state.OPcache = project.OPcache
}

// applyWeb loads the web server configuration when it changes. A project
// with invalid custom directives is left out so the others keep working;
// their errors are returned by main domain.
func (d *Daemon) applyWeb(sites []proxy.Site) map[string]string {
	options := proxy.Options{HTTPPort: d.options.HTTPPort, HTTPSPort: d.options.HTTPSPort, Storage: d.layout.Caddy()}
	rejected := map[string]string{}
	valid := make([]proxy.Site, 0, len(sites))
	for _, site := range sites {
		if site.Custom != "" {
			err := proxy.Validate(proxy.Caddyfile(options, []proxy.Site{site}))
			d.report("caddy "+site.Domains[0], err)
			if err != nil {
				rejected[site.Domains[0]] = "invalid caddy directives: " + err.Error()
				continue
			}
		}
		valid = append(valid, site)
	}
	caddyfile := proxy.Caddyfile(options, valid)
	if caddyfile == d.caddyfile {
		return rejected
	}
	err := proxy.Apply(caddyfile)
	d.report("web server", err)
	if err == nil {
		d.caddyfile = caddyfile
	}
	return rejected
}

// ensureServices keeps every installed service running and returns the web
// sites they expose and the state of each one.
func (d *Daemon) ensureServices() ([]proxy.Site, []status.Service) {
	available, missing := services.Available(d.layout)
	var states []status.Service
	for _, m := range missing {
		d.report("service "+m.Name, errors.New(m.Reason))
		states = append(states, status.Service{Name: m.Name, Error: m.Reason})
	}
	var sites []proxy.Site
	for _, service := range available {
		key := "service " + service.Name
		err := d.ensureService(service)
		d.report(key, err)
		state := status.Service{Name: service.Name, Port: service.Port, Log: d.logFile(service.Name), Running: err == nil}
		if err != nil {
			state.Error = err.Error()
		}
		if err == nil && service.WebDomain != "" {
			sites = append(sites, proxy.Site{Domains: []string{service.WebDomain}, Upstream: service.WebUpstream})
			state.WebURL = "https://" + service.WebDomain
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	return sites, states
}

func (d *Daemon) ensureService(service services.Service) error {
	if current := d.services[service.Name]; current != nil {
		if running(current) {
			return nil
		}
		delete(d.services, service.Name)
		if time.Since(current.started) < minUptime {
			d.retry[service.Name] = time.Now().Add(retryDelay)
		}
	}
	if until, ok := d.retry[service.Name]; ok && time.Now().Before(until) {
		return fmt.Errorf("stopped right after starting; see %s", d.logFile(service.Name))
	}
	if !services.PortFree(service.Port) {
		return fmt.Errorf("port %d is in use by another program", service.Port)
	}
	if service.Prepare != nil {
		if err := service.Prepare(); err != nil {
			return err
		}
	}
	logFile, err := os.OpenFile(d.logFile(service.Name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(service.Binary, service.Args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	started, err := start(cmd, "", service.StopTimeout)
	_ = logFile.Close()
	if err != nil {
		return fmt.Errorf("start %s: %w", service.Name, err)
	}
	d.services[service.Name] = started
	return nil
}

func (d *Daemon) logFile(name string) string {
	return filepath.Join(d.layout.Logs(), name+".log")
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
		d.stop(d.processes, name)
	}

	cmd := exec.Command(d.layout.PHPFPM(project.PHP), "-c", files.INI, "-y", files.Conf)
	cmd.Dir = dir
	started, err := start(cmd, files.Fingerprint, stopTimeout)
	if err != nil {
		return config.Project{}, fmt.Errorf("start php-fpm: %w", err)
	}
	d.processes[name] = started
	return project, nil
}

func start(cmd *exec.Cmd, fingerprint string, timeout time.Duration) (*process, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	started := &process{cmd: cmd, done: make(chan struct{}), fingerprint: fingerprint, started: time.Now(), stopTimeout: timeout}
	go func() {
		_ = cmd.Wait()
		close(started.done)
	}()
	return started, nil
}

func running(p *process) bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (d *Daemon) stop(group map[string]*process, name string) {
	current := group[name]
	delete(group, name)
	if current == nil || !running(current) {
		return
	}
	_ = current.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-current.done:
	case <-time.After(current.stopTimeout):
		_ = current.cmd.Process.Kill()
		<-current.done
	}
}

func (d *Daemon) stopAll() {
	for name := range d.processes {
		d.stop(d.processes, name)
	}
	for name := range d.services {
		d.stop(d.services, name)
	}
	if d.caddyfile != "" {
		_ = proxy.Stop()
	}
}

// report logs a problem once, until it changes or clears.
func (d *Daemon) report(key string, err error) {
	if err == nil {
		if _, had := d.problems[key]; had {
			log.Printf("%s: ok", key)
		}
		delete(d.problems, key)
		return
	}
	if d.problems[key] != err.Error() {
		d.problems[key] = err.Error()
		log.Printf("%s: %v", key, err)
	}
}

// docRoot uses public/ only when it holds the front page, as in Laravel;
// apps such as OJS keep uploads in a public/ folder instead.
func docRoot(dir, root string) string {
	if root != "" {
		return filepath.Join(dir, root)
	}
	public := filepath.Join(dir, "public")
	switch {
	case hasFile(public, "index.php"):
		return public
	case hasFile(dir, "index.php"):
		return dir
	case hasFile(public, "index.html"):
		return public
	}
	return dir
}

// isStatic reports whether a document root is a plain HTML site.
func isStatic(root string) bool {
	return !hasFile(root, "index.php") && hasFile(root, "index.html")
}

func hasFile(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
}
