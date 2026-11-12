package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

func archive(t *testing.T, version string, files ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "php-"+version+"-macos-aarch64.tar.gz")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	writer := tar.NewWriter(gz)
	defer writer.Close()
	for _, name := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: 1, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestInstallPHP(t *testing.T) {
	layout := paths.Layout{Root: t.TempDir()}
	version, err := InstallPHP(layout, archive(t, "8.4.27", "bin/php", "bin/php-fpm"))
	if err != nil {
		t.Fatal(err)
	}
	if version != "8.4" || !layout.HasPHP("8.4") {
		t.Errorf("version=%s installed=%v", version, layout.HasPHP("8.4"))
	}
}

func TestInstallPHPRejectsPathTraversal(t *testing.T) {
	layout := paths.Layout{Root: t.TempDir()}
	if _, err := InstallPHP(layout, archive(t, "8.4.27", "../../escaped")); err == nil {
		t.Fatal("expected an error for an escaping path")
	}
}

func TestInstallPHPKeepsExistingVersionOnBadArchive(t *testing.T) {
	layout := paths.Layout{Root: t.TempDir()}
	if _, err := InstallPHP(layout, archive(t, "8.4.27", "bin/php", "bin/php-fpm")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallPHP(layout, archive(t, "8.4.28", "bin/php")); err == nil {
		t.Fatal("expected an error for an incomplete archive")
	}
	if !layout.HasPHP("8.4") {
		t.Fatal("existing version was removed")
	}
}

func TestInstallKeepsInternalLinksAndRejectsEscapingOnes(t *testing.T) {
	build := func(link string) string {
		path := filepath.Join(t.TempDir(), "valkey.tar.gz")
		out, _ := os.Create(path)
		gz := gzip.NewWriter(out)
		writer := tar.NewWriter(gz)
		_ = writer.WriteHeader(&tar.Header{Name: "./", Mode: 0o755, Typeflag: tar.TypeDir})
		_ = writer.WriteHeader(&tar.Header{Name: "./bin/valkey-server", Mode: 0o755, Size: 1, Typeflag: tar.TypeReg})
		_, _ = writer.Write([]byte("x"))
		_ = writer.WriteHeader(&tar.Header{Name: "./bin/redis-server", Linkname: link, Typeflag: tar.TypeSymlink})
		writer.Close()
		gz.Close()
		out.Close()
		return path
	}
	layout := paths.Layout{Root: t.TempDir()}
	if err := Install(layout, "valkey", "9.1", "9.1.3", build("valkey-server")); err != nil {
		t.Fatal(err)
	}
	got := List(layout)
	if len(got) != 1 || got[0] != (Installed{Name: "valkey", Series: "9.1", Version: "9.1.3"}) {
		t.Errorf("unexpected list: %+v", got)
	}
	if err := Install(layout, "valkey", "9.1", "9.1.4", build("../../../../etc/passwd")); err == nil {
		t.Error("expected an error for a link leaving the runtime")
	}
	if got := List(layout); got[0].Version != "9.1.3" {
		t.Error("a rejected archive replaced the installed runtime")
	}
}

func TestCatalogFindsNewestSeries(t *testing.T) {
	file := filepath.Join(t.TempDir(), "runtimes.json")
	_ = os.WriteFile(file, []byte(`{"runtimes":[
		{"name":"mariadb","series":"10.11","version":"10.11.15"},
		{"name":"mariadb","series":"11.8","version":"11.8.9"},
		{"name":"mariadb","series":"11.4","version":"11.4.13"}]}`), 0o644)
	t.Setenv("STACKNEST_CATALOG", file)
	catalog, err := FetchCatalog(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if entry, _ := catalog.Find("mariadb", ""); entry.Series != "11.8" {
		t.Errorf("newest series = %s", entry.Series)
	}
	if entry, _ := catalog.Find("mariadb", "10.11"); entry.Version != "10.11.15" {
		t.Errorf("10.11 = %+v", entry)
	}
	if _, err := catalog.Find("mysql", ""); err == nil {
		t.Error("expected an error for a missing runtime")
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("payload")) }))
	defer server.Close()
	entry := Entry{File: "x.tar.gz", URL: server.URL, SHA256: "0000", Size: 7}
	if _, err := Download(context.Background(), server.Client(), entry, nil); err == nil {
		t.Fatal("expected a checksum error")
	}
	sum := sha256.Sum256([]byte("payload"))
	entry.SHA256 = hex.EncodeToString(sum[:])
	var last int
	path, err := Download(context.Background(), server.Client(), entry, func(p int) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if last != 100 {
		t.Errorf("progress ended at %d", last)
	}
}
