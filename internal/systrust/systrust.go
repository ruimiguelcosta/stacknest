// Package systrust checks and applies the two macOS settings that make
// https://*.test work in browsers: the /etc/resolver/test file pointing at
// StackNest's DNS port, and the local root CA trusted in the System keychain.
package systrust

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ResolverPath is the macOS per-domain resolver file for .test.
const ResolverPath = "/etc/resolver/test"

// Report says which of the two settings are missing.
type Report struct {
	ResolverOK   bool   `json:"resolver_ok"`
	ResolverInfo string `json:"resolver_info"`
	CATrusted    bool   `json:"ca_trusted"`
	CAInfo       string `json:"ca_info"`
}

// OK reports whether nothing is missing.
func (r Report) OK() bool { return r.ResolverOK && r.CATrusted }

// ParseResolver returns nameserver and port from a resolver file (port 53 when absent).
func ParseResolver(content string) (nameserver string, port int) {
	port = 53
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			if nameserver == "" {
				nameserver = fields[1]
			}
		case "port":
			if p, err := strconv.Atoi(fields[1]); err == nil {
				port = p
			}
		}
	}
	return nameserver, port
}

// ResolverContent is the file StackNest needs for its DNS port.
func ResolverContent(dnsPort int) string {
	return fmt.Sprintf("nameserver 127.0.0.1\nport %d\n", dnsPort)
}

// Check inspects the current machine.
func Check(dnsPort int, rootCA string) Report {
	var r Report
	data, err := os.ReadFile(ResolverPath)
	switch {
	case err != nil:
		r.ResolverInfo = ResolverPath + " does not exist"
	default:
		ns, port := ParseResolver(string(data))
		r.ResolverOK = ns == "127.0.0.1" && port == dnsPort
		if !r.ResolverOK {
			r.ResolverInfo = fmt.Sprintf("%s points to %s:%d, StackNest DNS is on 127.0.0.1:%d", ResolverPath, ns, port, dnsPort)
		}
	}

	if _, err := os.Stat(rootCA); err != nil {
		r.CAInfo = "root CA not created yet (start StackNest first): " + rootCA
		return r
	}
	// verify-cert succeeds only when the chain is trusted for SSL.
	if err := exec.Command("security", "verify-cert", "-c", rootCA, "-p", "ssl", "-q").Run(); err == nil {
		r.CATrusted = true
	} else {
		r.CAInfo = "root CA is not trusted in the keychain: " + rootCA
	}
	return r
}

// Script is the shell script that fixes whatever Check found missing. It is
// meant to run once as root (via an admin password prompt).
func Script(r Report, dnsPort int, rootCA string) string {
	var lines []string
	if !r.ResolverOK {
		lines = append(lines,
			"mkdir -p /etc/resolver",
			fmt.Sprintf("printf %s > %s", shellQuote(ResolverContent(dnsPort)), ResolverPath),
			"dscacheutil -flushcache",
			"killall -HUP mDNSResponder",
		)
	}
	if !r.CATrusted {
		lines = append(lines, fmt.Sprintf("security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s", shellQuote(rootCA)))
	}
	return strings.Join(lines, " && ")
}

// Apply runs Script behind the standard macOS administrator password dialog.
func Apply(script string) error {
	if script == "" {
		return nil
	}
	apple := fmt.Sprintf(`do shell script %s with prompt "O StackNest precisa de configurar os domínios .test e confiar no seu certificado local." with administrator privileges`, appleQuote(script))
	out, err := exec.Command("osascript", "-e", apple).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
