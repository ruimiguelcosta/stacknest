// Package runtime installs PHP builds and Composer.
package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

const composerURL = "https://getcomposer.org/download/latest-stable/composer.phar"

var archiveName = regexp.MustCompile(`^php-(\d+\.\d+)\.\d+-macos-aarch64\.tar\.gz$`)

// InstallPHP unpacks a release archive and returns its major.minor version.
// The new version only replaces an existing one once fully extracted.
func InstallPHP(layout paths.Layout, archive string) (string, error) {
	match := archiveName.FindStringSubmatch(filepath.Base(archive))
	if match == nil {
		return "", fmt.Errorf("unexpected archive name %q", filepath.Base(archive))
	}
	version := match[1]
	target := layout.Runtime(version)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	if err := extract(archive, staging); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(staging, "bin", "php-fpm")); err != nil {
		return "", errors.New("archive does not contain bin/php-fpm")
	}
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", err
	}
	return version, nil
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
		if path != dest && !strings.HasPrefix(path, dest+string(os.PathSeparator)) {
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
		default:
			return fmt.Errorf("unsupported entry in archive: %s", header.Name)
		}
	}
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
