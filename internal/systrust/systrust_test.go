package systrust

import (
	"strings"
	"testing"
)

func TestParseResolver(t *testing.T) {
	cases := []struct {
		in   string
		ns   string
		port int
	}{
		{"nameserver 127.0.0.1", "127.0.0.1", 53},
		{"nameserver 127.0.0.1\nport 5354\n", "127.0.0.1", 5354},
		{"# comment\nport 99\nnameserver 10.0.0.1\nnameserver 1.1.1.1", "10.0.0.1", 99},
		{"", "", 53},
	}
	for _, c := range cases {
		ns, port := ParseResolver(c.in)
		if ns != c.ns || port != c.port {
			t.Errorf("ParseResolver(%q) = %q,%d want %q,%d", c.in, ns, port, c.ns, c.port)
		}
	}
}

func TestScriptOnlyFixesResolver(t *testing.T) {
	if s := Script(Report{ResolverOK: true}, 5354); s != "" {
		t.Fatalf("expected empty script, got %q", s)
	}
	s := Script(Report{ResolverOK: false}, 5354)
	if !strings.Contains(s, "port 5354") || strings.Contains(s, "add-trusted-cert") {
		t.Fatalf("resolver script wrong: %q", s)
	}
}

func TestAppleQuote(t *testing.T) {
	if got := appleQuote(`printf 'a\nb' "x"`); got != `"printf 'a\\nb' \"x\""` {
		t.Fatalf("appleQuote = %s", got)
	}
}
