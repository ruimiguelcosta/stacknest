// Package launchd installs the LaunchAgent that keeps StackNest running.
package launchd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const Label = "dev.stacknest.daemon"

// Plist renders the agent definition. ExitTimeOut leaves time for the
// database to shut down cleanly before launchd kills it.
func Plist(executable, home, logFile string) string {
	escape := func(s string) string {
		var buf bytes.Buffer
		_ = xml.EscapeText(&buf, []byte(s))
		return buf.String()
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>STACKNEST_HOME</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ExitTimeOut</key>
	<integer>150</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, Label, escape(executable), escape(home), escape(logFile), escape(logFile))
}

// File is where the agent definition lives for the current user.
func File() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Install writes the agent and (re)starts it.
func Install(plist string) error {
	file, err := File()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte(plist), 0o644); err != nil {
		return err
	}
	_ = Stop()
	if output, err := exec.Command("launchctl", "bootstrap", domain(), file).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

// Stop unloads the agent and waits until it has exited; it is not an error if
// it was not loaded.
func Stop() error {
	service := domain() + "/" + Label
	if err := exec.Command("launchctl", "bootout", service).Run(); err != nil {
		return err
	}
	for deadline := time.Now().Add(160 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if exec.Command("launchctl", "print", service).Run() != nil {
			return nil
		}
	}
	return fmt.Errorf("%s did not stop in time", Label)
}

// Uninstall stops the agent and removes its definition.
func Uninstall() error {
	_ = Stop()
	file, err := File()
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
