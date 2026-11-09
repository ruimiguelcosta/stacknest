package runtime

import (
	"archive/tar"
	"compress/gzip"
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
