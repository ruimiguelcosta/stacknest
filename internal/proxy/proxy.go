// Package proxy runs the embedded Caddy server that fronts every project.
package proxy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

// PHPPlaceholder in Site.Custom expands to the site's PHP-FPM address.
const PHPPlaceholder = "{stacknest.php}"

// Site is either a PHP project (Root and Socket) or a reverse proxy (Upstream).
type Site struct {
	Domains  []string
	Root     string
	Socket   string
	Custom   string
	Upstream string
	// Static serves files only, for sites without PHP.
	Static bool
}

type Options struct {
	HTTPPort, HTTPSPort int
	Storage             string
}

// Caddyfile renders the configuration; sites are sorted so output is stable.
func Caddyfile(options Options, sites []Site) string {
	sorted := append([]Site(nil), sites...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domains[0] < sorted[j].Domains[0] })

	var out strings.Builder
	fmt.Fprintf(&out, `{
	admin off
	http_port %d
	https_port %d
	local_certs
	skip_install_trust
	storage file_system {
		root %q
	}
	log {
		level ERROR
	}
}
`, options.HTTPPort, options.HTTPSPort, options.Storage)
	for _, site := range sorted {
		fmt.Fprintf(&out, "\n%s {\n%s}\n", strings.Join(site.Domains, ", "), siteBody(site))
	}
	return out.String()
}

// localOnly drops requests from other machines. macOS lets an unprivileged
// process bind ports 80 and 443 only on every interface, not on loopback alone.
const localOnly = "\t@remote not remote_ip 127.0.0.1/8 ::1\n\tabort @remote\n"

func siteBody(site Site) string {
	if site.Upstream != "" {
		return localOnly + fmt.Sprintf("\treverse_proxy %s\n", site.Upstream)
	}
	php := "unix/" + site.Socket
	body := localOnly + fmt.Sprintf("\troot * %q\n\tencode zstd gzip\n", site.Root)
	if site.Custom != "" {
		return body + indent(strings.ReplaceAll(site.Custom, PHPPlaceholder, fmt.Sprintf("%q", php)))
	}
	if site.Static {
		return body + "\tfile_server\n"
	}
	return body + fmt.Sprintf("\tphp_fastcgi %q\n\tfile_server\n", php)
}

func indent(text string) string {
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out.WriteString("\t" + line + "\n")
	}
	return out.String()
}

// Validate checks that a Caddyfile adapts without loading it.
func Validate(caddyfile string) error {
	_, err := adapt(caddyfile)
	return err
}

// Apply loads a Caddyfile into the running server.
func Apply(caddyfile string) error {
	config, err := adapt(caddyfile)
	if err != nil {
		return err
	}
	return caddy.Load(config, true)
}

func adapt(caddyfile string) ([]byte, error) {
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return nil, fmt.Errorf("caddyfile adapter is not available")
	}
	config, _, err := adapter.Adapt([]byte(caddyfile), nil)
	if err != nil {
		return nil, fmt.Errorf("adapt caddyfile: %w", err)
	}
	return config, nil
}

func Stop() error { return caddy.Stop() }
