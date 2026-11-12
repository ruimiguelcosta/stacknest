package cve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const lock = `{"packages":[{"name":"laravel/framework","version":"v11.0.0"},{"name":"acme/branch","version":"dev-main"}],"packages-dev":[{"name":"phpunit/phpunit","version":"10.5.0"}]}`

func TestCachedScansOncePerDayAndMapsFindings(t *testing.T) {
	var batchCalls, nvdCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/batch":
			batchCalls.Add(1)
			var body struct {
				Queries []struct {
					Package struct{ Name string } `json:"package"`
					Version string                `json:"version"`
				} `json:"queries"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			results := make([]map[string]any, len(body.Queries))
			for i, q := range body.Queries {
				results[i] = map[string]any{}
				if q.Package.Name == "laravel/framework" && q.Version == "11.0.0" {
					results[i]["vulns"] = []map[string]string{{"id": "GHSA-aaaa"}}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		case r.URL.Path == "/vulns/GHSA-aaaa":
			_, _ = w.Write([]byte(`{"id":"GHSA-aaaa","summary":"Token bypass","aliases":["CVE-2026-1"],"published":"2026-10-01T00:00:00Z",
				"database_specific":{"severity":"CRITICAL"},
				"affected":[{"package":{"name":"laravel/framework"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"12.68.0"},{"introduced":"11.0.0"},{"fixed":"11.5.2"}]}]}]}`))
		case r.URL.Path == "/nvd":
			nvdCalls.Add(1)
			if !strings.Contains(r.URL.Query().Get("virtualMatchString"), "php:php:8.4.27") {
				t.Errorf("unexpected CPE %q", r.URL.Query().Get("virtualMatchString"))
			}
			_, _ = w.Write([]byte(`{"vulnerabilities":[{"cve":{"id":"CVE-2026-9","published":"2026-09-01T10:00:00.000",
				"descriptions":[{"lang":"en","value":"Heap overflow in PHP\nmore"}],
				"metrics":{"cvssMetricV31":[{"cvssData":{"baseScore":7.5,"baseSeverity":"HIGH"}}]}}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "composer.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	scanner := &Scanner{Client: server.Client(), Sleep: func(time.Duration) {}, OSVBatchURL: server.URL + "/batch", OSVVulnURL: server.URL + "/vulns/", NVDURL: server.URL + "/nvd"}
	projects := []Project{{Name: "blog", Domain: "blog.test", Dir: dir}, {Name: "empty", Domain: "empty.test", Dir: t.TempDir()}}
	runtimes := []Runtime{{Component: "PHP", Product: "php:php", Version: "8.4.27", Projects: []string{"blog"}}}
	cache := filepath.Join(t.TempDir(), "cves.json")

	report, err := scanner.Cached(context.Background(), cache, projects, runtimes, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Projects) != 2 || report.Projects[0].Packages != 2 || len(report.Projects[0].Findings) != 1 {
		t.Fatalf("unexpected report: %+v", report.Projects)
	}
	f := report.Projects[0].Findings[0]
	if f.ID != "CVE-2026-1" || f.Severity != "CRITICAL" || f.Fixed != "11.5.2" || f.Aliases[0] != "GHSA-aaaa" {
		t.Errorf("unexpected finding: %+v", f)
	}
	r := report.Runtimes[0]
	if len(r.Findings) != 1 || r.Findings[0].Score != 7.5 || r.Findings[0].Summary != "Heap overflow in PHP" {
		t.Errorf("unexpected runtime report: %+v", r)
	}

	if _, err := scanner.Cached(context.Background(), cache, projects, runtimes, false); err != nil {
		t.Fatal(err)
	}
	if batchCalls.Load() != 1 || nvdCalls.Load() != 1 {
		t.Errorf("cache not used: %d batch, %d nvd calls", batchCalls.Load(), nvdCalls.Load())
	}
	if _, err := scanner.Cached(context.Background(), cache, projects, runtimes, true); err != nil {
		t.Fatal(err)
	}
	if batchCalls.Load() != 2 {
		t.Error("refresh did not scan again")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"11.5.2", "11.0.0", 1},
		{"1.2.10", "1.2.9", 1},
		{"2.0", "2.0.0", 0},
		{"10.9.0", "11.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
