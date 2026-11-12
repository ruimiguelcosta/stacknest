package proxy

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func TestCaddyfileIsValidAndSorted(t *testing.T) {
	text := Caddyfile(Options{HTTPPort: 18080, HTTPSPort: 18443, Storage: "/tmp/with space"}, []Site{
		{Domains: []string{"zeta.test"}, Root: "/p/zeta/public", Socket: "/run/zeta.sock"},
		{Domains: []string{"alpha.test", "www.alpha.test"}, Root: "/p/my alpha/public", Socket: "/run/alpha.sock"},
		{Domains: []string{"mail.test"}, Upstream: "127.0.0.1:8026"},
		{Domains: []string{"static.test"}, Root: "/p/static", Socket: "/run/static.sock", Static: true},
		{Domains: []string{"custom.test"}, Root: "/p/custom", Socket: "/run/custom.sock", Custom: "php_fastcgi {stacknest.php} {\n\ttry_files {path} =404\n}\nfile_server\n"},
	})
	if strings.Index(text, "alpha.test") > strings.Index(text, "zeta.test") {
		t.Error("sites are not sorted")
	}
	config, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(text), nil)
	if err != nil {
		t.Fatalf("caddy rejected the configuration: %v\n%s", err, text)
	}
	for _, want := range []string{"www.alpha.test", "/p/my alpha/public", "unix//run/alpha.sock", "127.0.0.1:8026", "unix//run/custom.sock"} {
		if !strings.Contains(string(config), want) {
			t.Errorf("adapted config is missing %q", want)
		}
	}
	if strings.Count(text, "abort @remote") != 5 {
		t.Error("every site must reject remote clients")
	}
	if strings.Contains(text, "unix//run/static.sock") {
		t.Error("static sites must not use PHP")
	}
	if strings.Contains(text, PHPPlaceholder) {
		t.Error("placeholder was not expanded")
	}
}
