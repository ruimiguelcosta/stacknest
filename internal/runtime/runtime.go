// Package runtime installs the programs StackNest runs (PHP, databases and
// services) from its own catalog, and Composer.
package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

const (
	composerURL = "https://getcomposer.org/download/latest-stable/composer.phar"
	// CatalogURL lists every runtime build; STACKNEST_CATALOG overrides it with a URL or a local file.
	CatalogURL  = "https://github.com/ruimiguelcosta/stacknest/releases/download/runtimes/runtimes.json"
	versionFile = ".version"
)

// Programs must exist in an archive before it replaces an installed runtime.
var requiredProgram = map[string]string{
	"php":       "bin/php-fpm",
	"mariadb":   "bin/mariadbd",
	"mysql":     "bin/mysqld",
	"postgres":  "bin/postgres",
	"valkey":    "bin/valkey-server",
	"mailpit":   "bin/mailpit",
	"typesense": "bin/typesense-server",
}

// Entry is one downloadable build.
type Entry struct {
	Name    string `json:"name"`
	Series  string `json:"series"`
	Version string `json:"version"`
	File    string `json:"file"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

type Catalog struct {
	Generated time.Time `json:"generated"`
	Runtimes  []Entry   `json:"runtimes"`
}

// Installed describes a runtime on disk.
type Installed struct {
	Name    string `json:"name"`
	Series  string `json:"series"`
	Version string `json:"version"`
}

// FetchCatalog downloads the catalog, or reads it from a local path.
func FetchCatalog(ctx context.Context, client *http.Client) (Catalog, error) {
	source := CatalogURL
	if override := os.Getenv("STACKNEST_CATALOG"); override != "" {
		source = override
	}
	var data []byte
	var err error
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		data, err = get(ctx, client, source)
	} else {
		data, err = os.ReadFile(source)
	}
	if err != nil {
		return Catalog{}, fmt.Errorf("runtime catalog: %w", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("runtime catalog: %w", err)
	}
	sort.SliceStable(catalog.Runtimes, func(i, j int) bool {
		a, b := catalog.Runtimes[i], catalog.Runtimes[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return newer(a.Series, b.Series)
	})
	return catalog, nil
}

// Find returns the build of a series, or the newest series when series is empty.
func (c Catalog) Find(name, series string) (Entry, error) {
	for _, entry := range c.Runtimes {
		if entry.Name == name && (series == "" || entry.Series == series) {
			return entry, nil
		}
	}
	if series == "" {
		return Entry{}, fmt.Errorf("%s is not in the catalog", name)
	}
	return Entry{}, fmt.Errorf("%s %s is not in the catalog", name, series)
}

// Download fetches an entry to a temporary file, reports progress from 0 to
// 100, and verifies the checksum. The caller removes the file.
func Download(ctx context.Context, client *http.Client, entry Entry, progress func(percent int)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "stacknest")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", entry.URL, resp.Status)
	}
	out, err := os.CreateTemp("", "stacknest-*-"+entry.File)
	if err != nil {
		return "", err
	}
	total := entry.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	hash := sha256.New()
	counter := &progressWriter{total: total, report: progress, last: -1}
	_, err = io.Copy(io.MultiWriter(out, hash, counter), resp.Body)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && entry.SHA256 != "" && hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		err = fmt.Errorf("%s: checksum does not match", entry.File)
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

type progressWriter struct {
	total, written int64
	last           int
	report         func(int)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.written += int64(len(b))
	if p.report != nil && p.total > 0 {
		if percent := int(p.written * 100 / p.total); percent != p.last {
			p.last = percent
			p.report(percent)
		}
	}
	return len(b), nil
}

// Install unpacks an archive as runtimes/NAME/SERIES. The new files only
// replace an existing install once fully extracted and checked.
func Install(layout paths.Layout, name, series, version, archive string) error {
	required, known := requiredProgram[name]
	if !known {
		return fmt.Errorf("unknown runtime %q", name)
	}
	target := layout.RuntimeDir(name, series)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	if err := extract(archive, staging); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(staging, required)); err != nil {
		return fmt.Errorf("archive does not contain %s", required)
	}
	if err := os.WriteFile(filepath.Join(staging, versionFile), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return err
	}
	old := target + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(target, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.Rename(old, target)
		return err
	}
	return os.RemoveAll(old)
}

// Remove deletes an installed runtime; data directories are not touched.
func Remove(layout paths.Layout, name, series string) error {
	target := layout.RuntimeDir(name, series)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("%s %s is not installed", name, series)
	}
	return os.RemoveAll(target)
}

// List returns the installed runtimes, newest series first.
func List(layout paths.Layout) []Installed {
	var installed []Installed
	names, _ := os.ReadDir(filepath.Join(layout.Root, "runtimes"))
	for _, name := range names {
		if _, known := requiredProgram[name.Name()]; !known {
			continue
		}
		series, _ := os.ReadDir(filepath.Join(layout.Root, "runtimes", name.Name()))
		for _, s := range series {
			if strings.HasPrefix(s.Name(), ".") || strings.HasSuffix(s.Name(), ".old") {
				continue
			}
			dir := layout.RuntimeDir(name.Name(), s.Name())
			if _, err := os.Stat(filepath.Join(dir, requiredProgram[name.Name()])); err != nil {
				continue
			}
			version, _ := os.ReadFile(filepath.Join(dir, versionFile))
			installed = append(installed, Installed{Name: name.Name(), Series: s.Name(), Version: strings.TrimSpace(string(version))})
		}
	}
	sort.SliceStable(installed, func(i, j int) bool {
		if installed[i].Name != installed[j].Name {
			return installed[i].Name < installed[j].Name
		}
		return newer(installed[i].Series, installed[j].Series)
	})
	return installed
}

// newer orders version series such as 11.8 before 10.11.
func newer(a, b string) bool {
	va, errA := semver.NewVersion(a)
	vb, errB := semver.NewVersion(b)
	if errA != nil || errB != nil {
		return a > b
	}
	return va.GreaterThan(vb)
}

var archiveName = regexp.MustCompile(`^php-((\d+\.\d+)\.\d+)-macos-aarch64\.tar\.gz$`)

// InstallPHP installs a local PHP archive and returns its major.minor version.
func InstallPHP(layout paths.Layout, archive string) (string, error) {
	match := archiveName.FindStringSubmatch(filepath.Base(archive))
	if match == nil {
		return "", fmt.Errorf("unexpected archive name %q", filepath.Base(archive))
	}
	return match[2], Install(layout, "php", match[2], match[1], archive)
}

func extract(archive, dest string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path := filepath.Join(dest, header.Name)
		if !inside(dest, path) {
			return fmt.Errorf("unsafe path in archive: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(path, reader, os.FileMode(header.Mode)&0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links such as libfoo.dylib -> libfoo.1.dylib must stay inside the runtime.
			if filepath.IsAbs(header.Linkname) || !inside(dest, filepath.Join(filepath.Dir(path), header.Linkname)) {
				return fmt.Errorf("unsafe link in archive: %s -> %s", header.Name, header.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry in archive: %s", header.Name)
		}
	}
}

func inside(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}

func writeFile(path string, content io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, content); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// InstallComposer downloads the latest stable Composer and verifies its checksum.
func InstallComposer(ctx context.Context, client *http.Client, layout paths.Layout) error {
	phar, err := get(ctx, client, composerURL)
	if err != nil {
		return err
	}
	sum, err := get(ctx, client, composerURL+".sha256sum")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(sum))
	if len(fields) == 0 {
		return errors.New("empty composer checksum")
	}
	actual := sha256.Sum256(phar)
	if hex.EncodeToString(actual[:]) != fields[0] {
		return errors.New("composer checksum does not match")
	}
	if err := os.MkdirAll(filepath.Dir(layout.Composer()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(layout.Composer(), phar, 0o644)
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stacknest")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
