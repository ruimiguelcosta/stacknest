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

// Script is the root shell script for the resolver file ("" when it is fine).
// The CA is not trusted here: since macOS 11 SecTrustSettingsSetTrustSettings
// refuses to run as root without user interaction, so TrustCA does it as the
// user instead.
func Script(r Report, dnsPort int) string {
	if r.ResolverOK {
		return ""
	}
	return strings.Join([]string{
		"mkdir -p /etc/resolver",
		fmt.Sprintf("printf %s > %s", shellQuote(ResolverContent(dnsPort)), ResolverPath),
		"dscacheutil -flushcache",
		"killall -HUP mDNSResponder",
	}, " && ")
}

// Apply fixes what Check found missing: the resolver behind the macOS
// administrator dialog, then the CA in the user's trust settings.
func Apply(r Report, dnsPort int, rootCA string) error {
	if script := Script(r, dnsPort); script != "" {
		apple := fmt.Sprintf(`do shell script %s with prompt "O StackNest precisa de ligar os domínios .test." with administrator privileges`, appleQuote(script))
		if out, err := exec.Command("osascript", "-e", apple).CombinedOutput(); err != nil {
			return fmt.Errorf("resolver: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if !r.CATrusted {
		return TrustCA(rootCA)
	}
	return nil
}

// TrustCA marks rootCA as a trusted SSL root for the current user. macOS shows
// its own authorization dialog (password or Touch ID).
func TrustCA(rootCA string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	keychain := home + "/Library/Keychains/login.keychain-db"
	out, err := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-k", keychain, rootCA).CombinedOutput()
	if err != nil {
		return fmt.Errorf("trust CA: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
