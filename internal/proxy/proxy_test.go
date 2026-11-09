package proxy

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func TestCaddyfileIsValidAndSorted(t *testing.T) {
	text := Caddyfile(Options{HTTPPort: 18080, HTTPSPort: 18443, Storage: "/tmp/with space"}, []Site{
		{Domain: "zeta.test", Root: "/p/zeta/public", Socket: "/run/zeta.sock"},
		{Domain: "alpha.test", Root: "/p/my alpha/public", Socket: "/run/alpha.sock"},
	})
	if strings.Index(text, "alpha.test") > strings.Index(text, "zeta.test") {
		t.Error("sites are not sorted")
	}
	config, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(text), nil)
	if err != nil {
		t.Fatalf("caddy rejected the configuration: %v\n%s", err, text)
	}
	for _, want := range []string{"alpha.test", "/p/my alpha/public", "unix//run/alpha.sock"} {
		if !strings.Contains(string(config), want) {
			t.Errorf("adapted config is missing %q", want)
		}
	}
}
