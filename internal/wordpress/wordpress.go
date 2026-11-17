// Package wordpress creates WordPress sites: it downloads the official
// release (checked against wordpress.org's SHA-1), writes wp-config.php for a
// StackNest database and runs the installer.
package wordpress

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Offer is the latest release for a locale.
type Offer struct {
	Version    string
	Locale     string
	Download   string // .tar.gz
	MinimumPHP string
}

var versionCheck = "https://api.wordpress.org/core/version-check/1.7/"

// Latest asks wordpress.org for the newest release in locale ("" = en_US).
func Latest(ctx context.Context, client *http.Client, locale string) (Offer, error) {
	query := url.Values{}
	if locale != "" {
		query.Set("locale", locale)
	}
	body, err := get(ctx, client, versionCheck+"?"+query.Encode())
	if err != nil {
		return Offer{}, err
	}
	var payload struct {
		Offers []struct {
			Locale     string `json:"locale"`
			Version    string `json:"version"`
			Download   string `json:"download"`
			PHPVersion string `json:"php_version"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Offer{}, fmt.Errorf("wordpress.org version check: %w", err)
	}
	want := locale
	if want == "" {
		want = "en_US"
	}
	for _, o := range payload.Offers {
		if o.Locale == want {
			return Offer{
				Version:    o.Version,
				Locale:     o.Locale,
				Download:   strings.TrimSuffix(o.Download, ".zip") + ".tar.gz",
				MinimumPHP: o.PHPVersion,
			}, nil
		}
	}
	return Offer{}, fmt.Errorf("wordpress.org has no %s release (check the locale, e.g. pt_PT)", want)
}

// Download fetches the release, verifies its SHA-1 and extracts it into dir
// (which must not exist), dropping the archive's top-level "wordpress/" folder.
func Download(ctx context.Context, client *http.Client, offer Offer, dir string) error {
	sum, err := get(ctx, client, offer.Download+".sha1")
	if err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	want := strings.TrimSpace(string(sum))
	archive, err := get(ctx, client, offer.Download)
	if err != nil {
		return err
	}
	got := sha1.Sum(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s", offer.Download)
	}
	return extract(strings.NewReader(string(archive)), dir)
}

func extract(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	base, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		_, rel, ok := strings.Cut(header.Name, "/")
		if !ok || rel == "" {
			continue
		}
		target := filepath.Join(base, filepath.FromSlash(rel))
		if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, reader); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
	}
}

// DB holds the wp-config.php database settings.
type DB struct {
	Name, User, Password, Host string
}

var (
	defineRE = regexp.MustCompile(`define\(\s*'(DB_NAME|DB_USER|DB_PASSWORD|DB_HOST)',\s*'[^']*'\s*\);`)
	// Matched by key, because translated samples translate the placeholder text.
	saltRE = regexp.MustCompile(`define\(\s*'(AUTH_KEY|SECURE_AUTH_KEY|LOGGED_IN_KEY|NONCE_KEY|AUTH_SALT|SECURE_AUTH_SALT|LOGGED_IN_SALT|NONCE_SALT)',\s*'[^']*'\s*\);`)
	// The ABSPATH guard is code, so it is the same in every translation.
	abspathLine = "if ( ! defined( 'ABSPATH' ) ) {"
)

// Config turns wp-config-sample.php into wp-config.php.
func Config(sample string, db DB, home string) (string, error) {
	if !strings.Contains(sample, abspathLine) || len(saltRE.FindAllString(sample, -1)) != 8 {
		return "", errors.New("wp-config-sample.php has an unexpected format")
	}
	values := map[string]string{"DB_NAME": db.Name, "DB_USER": db.User, "DB_PASSWORD": db.Password, "DB_HOST": db.Host}
	out := defineRE.ReplaceAllStringFunc(sample, func(match string) string {
		key := defineRE.FindStringSubmatch(match)[1]
		return fmt.Sprintf("define( '%s', '%s' );", key, phpEscape(values[key]))
	})
	var saltErr error
	out = saltRE.ReplaceAllStringFunc(out, func(match string) string {
		salt, err := Salt()
		if err != nil {
			saltErr = err
		}
		return fmt.Sprintf("define( '%s', '%s' );", saltRE.FindStringSubmatch(match)[1], phpEscape(salt))
	})
	if saltErr != nil {
		return "", saltErr
	}
	extra := fmt.Sprintf("define( 'WP_ENVIRONMENT_TYPE', 'local' );\ndefine( 'WP_HOME', '%[1]s' );\ndefine( 'WP_SITEURL', '%[1]s' );\n\n", phpEscape(home))
	return strings.Replace(out, abspathLine, extra+abspathLine, 1), nil
}

const saltChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_[]{}<>~+=,.;:/?|"

// Salt is a 64-character random key like the ones from api.wordpress.org/secret-key.
func Salt() (string, error) {
	return random(saltChars, 64)
}

// Password is a random admin password without ambiguous symbols.
func Password() (string, error) {
	return random("abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789", 20)
}

func random(alphabet string, n int) (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < n; i++ {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[k.Int64()])
	}
	return b.String(), nil
}

func phpEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// MailPlugin is a must-use plugin that sends wp_mail() to StackNest's mailbox.
func MailPlugin(port int) string {
	return fmt.Sprintf(`<?php
/**
 * Plugin Name: StackNest mail
 * Description: Sends wp_mail() to the local StackNest mailbox (https://mail.test). Created by stacknest new --wordpress.
 */
add_action( 'phpmailer_init', function ( $mailer ) {
	$mailer->isSMTP();
	$mailer->Host     = '127.0.0.1';
	$mailer->Port     = %d;
	$mailer->SMTPAuth = false;
	$mailer->SMTPAutoTLS = false;
} );
`, port)
}

// InstallScript is run with the project's PHP: argv = [script, wordpress dir, title, admin user, admin email, password, home].
const InstallScript = `<?php
define( 'WP_INSTALLING', true );
$_SERVER['HTTP_HOST'] = parse_url( $argv[6], PHP_URL_HOST );
$_SERVER['HTTPS']     = 'on';
require $argv[1] . '/wp-load.php';
require_once ABSPATH . 'wp-admin/includes/upgrade.php';
if ( is_blog_installed() ) {
	fwrite( STDERR, "WordPress is already installed\n" );
	exit( 1 );
}
$result = wp_install( $argv[2], $argv[3], $argv[4], false, '', $argv[5] );
update_option( 'permalink_structure', '/%postname%/' );
flush_rewrite_rules( true );
echo "installed\n";
`

func get(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "StackNest")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", target, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
