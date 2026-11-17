package wordpress

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `<?php
define( 'DB_NAME', 'database_name_here' );
define( 'DB_USER', 'username_here' );
define( 'DB_PASSWORD', 'password_here' );
define( 'DB_HOST', 'localhost' );
define( 'AUTH_KEY',         'digite aqui a sua frase única' );
define( 'SECURE_AUTH_KEY',  'digite aqui a sua frase única' );
define( 'LOGGED_IN_KEY',    'digite aqui a sua frase única' );
define( 'NONCE_KEY',        'digite aqui a sua frase única' );
define( 'AUTH_SALT',        'digite aqui a sua frase única' );
define( 'SECURE_AUTH_SALT', 'digite aqui a sua frase única' );
define( 'LOGGED_IN_SALT',   'digite aqui a sua frase única' );
define( 'NONCE_SALT',       'digite aqui a sua frase única' );
define( 'WP_DEBUG', false );
/* E é tudo, pare de editar! */
if ( ! defined( 'ABSPATH' ) ) {
	define( 'ABSPATH', __DIR__ . '/' );
}
require_once ABSPATH . 'wp-settings.php';
`

func TestConfig(t *testing.T) {
	out, err := Config(sample, DB{Name: "blog", User: "root", Password: "it's", Host: "127.0.0.1:3306"}, "https://blog.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"define( 'DB_NAME', 'blog' );",
		"define( 'DB_PASSWORD', 'it\\'s' );",
		"define( 'DB_HOST', '127.0.0.1:3306' );",
		"define( 'WP_HOME', 'https://blog.test' );",
		"define( 'WP_ENVIRONMENT_TYPE', 'local' );",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "frase única") {
		t.Error("salts were not replaced")
	}
	if strings.Index(out, "WP_HOME") > strings.Index(out, "wp-settings.php") {
		t.Error("WP_HOME must come before wp-settings.php is loaded")
	}
	if _, err := Config("<?php", DB{}, "https://x.test"); err == nil {
		t.Error("expected an error for an unknown sample")
	}
}

func TestSaltsDiffer(t *testing.T) {
	a, _ := Salt()
	b, _ := Salt()
	if len(a) != 64 || a == b {
		t.Fatalf("bad salts %q %q", a, b)
	}
}

func tarball(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestDownloadVerifiesAndStrips(t *testing.T) {
	archive := tarball(t, map[string]string{"wordpress/index.php": "<?php", "wordpress/wp-admin/a.php": "x"})
	sum := sha1.Sum(archive)
	good := hex.EncodeToString(sum[:])
	checksum := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha1") {
			w.Write([]byte(checksum + "\n"))
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "site")
	offer := Offer{Download: srv.URL + "/wordpress.tar.gz"}
	if err := Download(context.Background(), srv.Client(), offer, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wp-admin", "a.php")); err != nil {
		t.Fatal("expected stripped path:", err)
	}

	checksum = strings.Repeat("0", 40)
	if err := Download(context.Background(), srv.Client(), offer, filepath.Join(t.TempDir(), "x")); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	archive := tarball(t, map[string]string{"wordpress/../../evil.php": "x"})
	if err := extract(bytes.NewReader(archive), filepath.Join(t.TempDir(), "s")); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestLatestPicksLocale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"offers":[{"locale":"pt_PT","version":"7.1.4","download":"https://d/release/pt_PT/wordpress-7.1.4.zip","php_version":"7.4"},{"locale":"en_US","version":"7.1.4","download":"https://d/wordpress-7.1.4.zip","php_version":"7.4"}]}`))
	}))
	defer srv.Close()
	versionCheck = srv.URL
	offer, err := Latest(context.Background(), srv.Client(), "pt_PT")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Download != "https://d/release/pt_PT/wordpress-7.1.4.tar.gz" || offer.MinimumPHP != "7.4" {
		t.Fatalf("unexpected offer %+v", offer)
	}
	if _, err := Latest(context.Background(), srv.Client(), "xx_YY"); err == nil {
		t.Fatal("expected unknown locale error")
	}
}
