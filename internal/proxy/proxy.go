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

type Site struct {
	Domain string
	Root   string
	Socket string
}

type Options struct {
	HTTPPort, HTTPSPort int
	Storage             string
}

// Caddyfile renders the configuration; sites are sorted so output is stable.
func Caddyfile(options Options, sites []Site) string {
	sorted := append([]Site(nil), sites...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domain < sorted[j].Domain })

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
		fmt.Fprintf(&out, `
%s {
	root * %q
	encode zstd gzip
	php_fastcgi %q
	file_server
}
`, site.Domain, site.Root, "unix/"+site.Socket)
	}
	return out.String()
}

// Apply loads a Caddyfile into the running server.
func Apply(caddyfile string) error {
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("caddyfile adapter is not available")
	}
	config, _, err := adapter.Adapt([]byte(caddyfile), nil)
	if err != nil {
		return fmt.Errorf("adapt caddyfile: %w", err)
	}
	return caddy.Load(config, true)
}

func Stop() error { return caddy.Stop() }
