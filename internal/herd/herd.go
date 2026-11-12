// Package herd reads the sites a Laravel Herd installation serves, so they can
// be moved to StackNest.
package herd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// herdFrontController marks the nginx files Herd generates itself.
const herdFrontController = "valet/server.php"

// Site is one project directory with every domain Herd served it under.
type Site struct {
	Dir     string
	Domains []string
	// PHP is the version Herd used, as "8.4"; empty when unknown.
	PHP string
	// CustomConfig is the path of a hand-written nginx file whose rules need review.
	CustomConfig string
}

var (
	socketVersion = regexp.MustCompile(`herd(?:_sock_)?(\d)(\d)(?:\.sock|\b)`)
	binaryVersion = regexp.MustCompile(`php(\d)(\d)$`)
	serverName    = regexp.MustCompile(`(?m)^\s*server_name\s+([^;]+);`)
	rootDirective = regexp.MustCompile(`(?m)^\s*root\s+"?([^";]+)"?;`)
)

// DefaultRoot is Herd's configuration folder.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Herd"), nil
}

// Sites lists the sites in a Herd configuration folder, sorted by domain,
// and describes the entries it could not use.
func Sites(root string) (sites []Site, skipped []string, err error) {
	valet := filepath.Join(root, "config", "valet")
	var settings struct {
		Paths []string `json:"paths"`
		TLD   string   `json:"tld"`
	}
	data, err := os.ReadFile(filepath.Join(valet, "config.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read Herd settings: %w", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, nil, fmt.Errorf("read Herd settings: %w", err)
	}
	if settings.TLD == "" {
		settings.TLD = "test"
	}

	byDir := map[string]*Site{}
	add := func(dir, domain string) {
		site := byDir[dir]
		if site == nil {
			site = &Site{Dir: dir}
			byDir[dir] = site
		}
		site.Domains = appendUnique(site.Domains, domain)
	}

	// Parked folders serve each subfolder; the Sites folder holds links.
	for _, parked := range settings.Paths {
		entries, err := os.ReadDir(parked)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			dir, err := filepath.EvalSymlinks(filepath.Join(parked, entry.Name()))
			if err != nil {
				target, _ := os.Readlink(filepath.Join(parked, entry.Name()))
				skipped = appendUnique(skipped, fmt.Sprintf("%s.%s: folder %s does not exist", strings.ToLower(entry.Name()), settings.TLD, target))
				continue
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() || herdOwned(root, dir) {
				continue
			}
			add(dir, strings.ToLower(entry.Name())+"."+settings.TLD)
		}
	}

	// Herd writes an nginx file per secured or isolated site, with its PHP
	// version and extra domains. Hand-written files can also add new sites.
	nginxFiles, _ := filepath.Glob(filepath.Join(valet, "Nginx", "*"))
	for _, file := range nginxFiles {
		domain := filepath.Base(file)
		text, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		generated := strings.Contains(string(text), herdFrontController)
		dir := ""
		for d, site := range byDir {
			if contains(site.Domains, domain) {
				dir = d
			}
		}
		if dir == "" {
			// Generated files for removed sites, or for subdomains a wildcard already covers.
			if generated {
				continue
			}
			match := rootDirective.FindSubmatch(text)
			if match == nil {
				continue
			}
			dir = string(match[1])
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				dir = resolved
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				skipped = appendUnique(skipped, fmt.Sprintf("%s: folder %s does not exist", domain, dir))
				continue
			}
		}
		add(dir, domain)
		site := byDir[dir]
		if !generated {
			site.CustomConfig = file
		}
		if match := socketVersion.FindSubmatch(text); match != nil {
			site.PHP = string(match[1]) + "." + string(match[2])
		}
		for _, names := range serverName.FindAllSubmatch(text, -1) {
			for _, name := range strings.Fields(string(names[1])) {
				if strings.HasSuffix(name, "."+settings.TLD) {
					add(dir, name)
				}
			}
		}
	}

	defaultPHP := herdDefaultPHP(root)
	sites = make([]Site, 0, len(byDir))
	for _, site := range byDir {
		if site.PHP == "" {
			site.PHP = defaultPHP
		}
		sort.Slice(site.Domains, func(i, j int) bool { return domainLess(site.Domains[i], site.Domains[j]) })
		sites = append(sites, *site)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Domains[0] < sites[j].Domains[0] })
	sort.Strings(skipped)
	return sites, skipped, nil
}

// domainLess puts the shortest name first, so app.test comes before www.app.test.
func domainLess(a, b string) bool {
	if strings.Count(a, ".") != strings.Count(b, ".") {
		return strings.Count(a, ".") < strings.Count(b, ".")
	}
	return a < b
}

func herdDefaultPHP(root string) string {
	target, err := os.Readlink(filepath.Join(root, "bin", "php"))
	if err != nil {
		return ""
	}
	if match := binaryVersion.FindStringSubmatch(target); match != nil {
		return match[1] + "." + match[2]
	}
	return ""
}

// herdOwned reports whether a site is part of Herd itself, such as its dashboards.
func herdOwned(root, dir string) bool {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	for _, owned := range []string{root, "/Users/Shared/Herd"} {
		if dir == owned || strings.HasPrefix(dir, owned+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func appendUnique(list []string, value string) []string {
	if contains(list, value) {
		return list
	}
	return append(list, value)
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
