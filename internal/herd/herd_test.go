package herd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const generatedNginx = `server {
  server_name %s www.%s *.%s;
  root /;
  location / { rewrite ^ "/Applications/Herd.app/Contents/Resources/valet/server.php" last; }
  fastcgi_pass $herd_sock_83;
}
`

func TestSitesMergesLinksParkedFoldersAndNginxFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Herd")
	valet := filepath.Join(root, "config", "valet")
	sitesDir := filepath.Join(valet, "Sites")
	parked := filepath.Join(base, "parked")
	projects := filepath.Join(base, "projects")
	for _, dir := range []string{sitesDir, filepath.Join(valet, "Nginx"), filepath.Join(root, "bin"), filepath.Join(parked, "blog"), filepath.Join(projects, "shop"), filepath.Join(projects, "moodle"), filepath.Join(projects, "saas"), filepath.Join(root, "dashboard")} {
		mustMkdir(t, dir)
	}
	mustWrite(t, filepath.Join(valet, "config.json"), `{"paths":["`+sitesDir+`","`+sitesDir+`/","`+parked+`"],"tld":"test"}`)
	mustSymlink(t, filepath.Join(root, "bin", "php85"), filepath.Join(root, "bin", "php"))
	mustSymlink(t, filepath.Join(projects, "shop"), filepath.Join(sitesDir, "shop"))
	mustSymlink(t, filepath.Join(projects, "shop"), filepath.Join(sitesDir, "store"))
	mustSymlink(t, filepath.Join(projects, "saas"), filepath.Join(sitesDir, "saas"))
	mustSymlink(t, filepath.Join(projects, "gone"), filepath.Join(sitesDir, "gone"))
	mustSymlink(t, filepath.Join(root, "dashboard"), filepath.Join(sitesDir, "dashboard.herd"))
	mustWrite(t, filepath.Join(valet, "Nginx", "shop.test"), "server {\n  server_name shop.test www.shop.test;\n  fastcgi_pass unix:/x/herd84.sock;\n}\n")
	mustWrite(t, filepath.Join(valet, "Nginx", "moodle.test"), "server {\n  server_name moodle.test;\n  root "+filepath.Join(projects, "moodle")+";\n  fastcgi_pass unix:/x/herd83.sock;\n}\n")
	mustWrite(t, filepath.Join(valet, "Nginx", "saas.test"), sprintf(generatedNginx, "saas.test"))
	mustWrite(t, filepath.Join(valet, "Nginx", "gone.test"), sprintf(generatedNginx, "gone.test"))
	mustWrite(t, filepath.Join(valet, "Nginx", "tenant.saas.test"), sprintf(generatedNginx, "tenant.saas.test"))

	got, skipped, err := Sites(root)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }
	want := []Site{
		{Dir: resolve(filepath.Join(parked, "blog")), Domains: []string{"blog.test"}, PHP: "8.5"},
		{Dir: resolve(filepath.Join(projects, "moodle")), Domains: []string{"moodle.test"}, PHP: "8.3", CustomConfig: filepath.Join(valet, "Nginx", "moodle.test")},
		{Dir: resolve(filepath.Join(projects, "saas")), Domains: []string{"saas.test", "*.saas.test", "www.saas.test"}, PHP: "8.3"},
		{Dir: resolve(filepath.Join(projects, "shop")), Domains: []string{"shop.test", "store.test", "www.shop.test"}, PHP: "8.4", CustomConfig: filepath.Join(valet, "Nginx", "shop.test")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if len(skipped) != 1 {
		t.Errorf("expected only the broken link to be skipped, got %v", skipped)
	}
}

func sprintf(format, domain string) string {
	return fmt.Sprintf(format, domain, domain, domain)
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, file, text string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
