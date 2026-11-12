// Package cve finds published vulnerabilities that affect the exact versions
// each project uses: Composer packages through OSV.dev, and the PHP and
// database servers through the NVD.
package cve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	osvBatchURL = "https://api.osv.dev/v1/querybatch"
	osvVulnURL  = "https://api.osv.dev/v1/vulns/"
	nvdURL      = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	osvBatchMax = 1000
	// Without an API key the NVD allows 5 requests per 30 seconds.
	nvdDelay = 6500 * time.Millisecond
	// MaxAge is how long a report is reused when nothing it covers changed.
	MaxAge = 24 * time.Hour
)

// Finding is one advisory affecting one component version.
type Finding struct {
	ID        string    `json:"id"`
	Aliases   []string  `json:"aliases,omitempty"`
	Component string    `json:"component"`
	Version   string    `json:"version"`
	Severity  string    `json:"severity"`
	Score     float64   `json:"score,omitempty"`
	Summary   string    `json:"summary"`
	Published time.Time `json:"published"`
	Fixed     string    `json:"fixed,omitempty"`
	URL       string    `json:"url"`
	Source    string    `json:"source"`
}

// Project is a site and the Composer packages it has locked.
type Project struct {
	Name, Domain, Dir string
}

// Runtime is a server program shared by several projects.
type Runtime struct {
	Component string   `json:"component"`
	Product   string   `json:"-"` // CPE vendor:product, such as php:php
	Version   string   `json:"version"`
	Projects  []string `json:"projects"`
}

type ProjectReport struct {
	Name     string    `json:"name"`
	Domain   string    `json:"domain"`
	Dir      string    `json:"dir"`
	Packages int       `json:"packages"`
	Findings []Finding `json:"findings"`
	Error    string    `json:"error,omitempty"`
}

type RuntimeReport struct {
	Runtime
	Findings []Finding `json:"findings"`
	Error    string    `json:"error,omitempty"`
}

type Report struct {
	Generated   time.Time       `json:"generated"`
	Fingerprint string          `json:"fingerprint"`
	Projects    []ProjectReport `json:"projects"`
	Runtimes    []RuntimeReport `json:"runtimes"`
}

type lockedPackage struct {
	Name, Version string
}

// Scanner talks to the vulnerability databases.
type Scanner struct {
	Client *http.Client
	// Sleep waits between NVD requests; tests replace it.
	Sleep func(time.Duration)
	// URLs can be pointed at test servers.
	OSVBatchURL, OSVVulnURL, NVDURL string
}

func NewScanner(client *http.Client) *Scanner {
	return &Scanner{Client: client, Sleep: time.Sleep, OSVBatchURL: osvBatchURL, OSVVulnURL: osvVulnURL, NVDURL: nvdURL}
}

// Cached returns the report in cacheFile when it is recent and covers the same
// inputs; otherwise it scans and saves a new one.
func (s *Scanner) Cached(ctx context.Context, cacheFile string, projects []Project, runtimes []Runtime, refresh bool) (Report, error) {
	locks := map[string][]lockedPackage{}
	lockErrors := map[string]string{}
	for _, project := range projects {
		packages, err := readLock(project.Dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			lockErrors[project.Name] = err.Error()
		}
		locks[project.Name] = packages
	}
	fingerprint := fingerprintOf(projects, locks, runtimes)

	if !refresh {
		if cached, err := load(cacheFile); err == nil && cached.Fingerprint == fingerprint && time.Since(cached.Generated) < MaxAge {
			return cached, nil
		}
	}
	report := s.scan(ctx, projects, locks, lockErrors, runtimes)
	report.Fingerprint = fingerprint
	if err := save(cacheFile, report); err != nil {
		return report, err
	}
	return report, nil
}

func (s *Scanner) scan(ctx context.Context, projects []Project, locks map[string][]lockedPackage, lockErrors map[string]string, runtimes []Runtime) Report {
	report := Report{Generated: time.Now().UTC()}

	// Query each package version once, however many projects lock it.
	var unique []lockedPackage
	seen := map[lockedPackage]bool{}
	for _, packages := range locks {
		for _, p := range packages {
			if !seen[p] {
				seen[p] = true
				unique = append(unique, p)
			}
		}
	}
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].Name+unique[i].Version < unique[j].Name+unique[j].Version
	})
	byPackage, osvErr := s.queryOSV(ctx, unique)

	for _, project := range projects {
		pr := ProjectReport{Name: project.Name, Domain: project.Domain, Dir: project.Dir, Packages: len(locks[project.Name]), Findings: []Finding{}}
		pr.Error = lockErrors[project.Name]
		if osvErr != nil && pr.Packages > 0 {
			pr.Error = osvErr.Error()
		}
		for _, p := range locks[project.Name] {
			pr.Findings = append(pr.Findings, byPackage[p]...)
		}
		sortFindings(pr.Findings)
		report.Projects = append(report.Projects, pr)
	}

	for i, runtime := range runtimes {
		if i > 0 {
			s.Sleep(nvdDelay)
		}
		rr := RuntimeReport{Runtime: runtime, Findings: []Finding{}}
		findings, err := s.queryNVD(ctx, runtime)
		if err != nil {
			rr.Error = err.Error()
		} else {
			rr.Findings = findings
		}
		sortFindings(rr.Findings)
		report.Runtimes = append(report.Runtimes, rr)
	}
	return report
}

// queryOSV returns the findings for each package version.
func (s *Scanner) queryOSV(ctx context.Context, packages []lockedPackage) (map[lockedPackage][]Finding, error) {
	ids := map[lockedPackage][]string{}
	for start := 0; start < len(packages); start += osvBatchMax {
		end := min(start+osvBatchMax, len(packages))
		type query struct {
			Package struct {
				Name      string `json:"name"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Version string `json:"version"`
		}
		var body struct {
			Queries []query `json:"queries"`
		}
		for _, p := range packages[start:end] {
			var q query
			q.Package.Name, q.Package.Ecosystem, q.Version = p.Name, "Packagist", p.Version
			body.Queries = append(body.Queries, q)
		}
		var result struct {
			Results []struct {
				Vulns []struct {
					ID string `json:"id"`
				} `json:"vulns"`
			} `json:"results"`
		}
		if err := s.postJSON(ctx, s.OSVBatchURL, body, &result); err != nil {
			return nil, fmt.Errorf("osv.dev: %w", err)
		}
		for i, r := range result.Results {
			for _, v := range r.Vulns {
				ids[packages[start+i]] = append(ids[packages[start+i]], v.ID)
			}
		}
	}

	details := map[string]osvVuln{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	limit := make(chan struct{}, 8)
	for _, list := range ids {
		for _, id := range list {
			mu.Lock()
			_, done := details[id]
			if !done {
				details[id] = osvVuln{}
			}
			mu.Unlock()
			if done {
				continue
			}
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				limit <- struct{}{}
				defer func() { <-limit }()
				var v osvVuln
				err := s.getJSON(ctx, s.OSVVulnURL+url.PathEscape(id), &v)
				mu.Lock()
				defer mu.Unlock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("osv.dev %s: %w", id, err)
				}
				details[id] = v
			}(id)
		}
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	findings := map[lockedPackage][]Finding{}
	for p, list := range ids {
		for _, id := range list {
			findings[p] = append(findings[p], details[id].finding(p))
		}
	}
	return findings, nil
}

type osvVuln struct {
	ID        string    `json:"id"`
	Summary   string    `json:"summary"`
	Details   string    `json:"details"`
	Aliases   []string  `json:"aliases"`
	Published time.Time `json:"published"`
	Severity  []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Name string `json:"name"`
		} `json:"package"`
		Ranges []struct {
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
}

func (v osvVuln) finding(p lockedPackage) Finding {
	f := Finding{
		ID:        v.ID,
		Aliases:   v.Aliases,
		Component: p.Name,
		Version:   p.Version,
		Severity:  normaliseSeverity(v.DatabaseSpecific.Severity),
		Summary:   firstNonEmpty(v.Summary, firstLine(v.Details)),
		Published: v.Published,
		URL:       "https://osv.dev/vulnerability/" + v.ID,
		Source:    "OSV",
	}
	// Prefer the CVE number people search for.
	for _, alias := range v.Aliases {
		if strings.HasPrefix(alias, "CVE-") {
			f.ID = alias
			f.Aliases = append([]string{v.ID}, without(v.Aliases, alias)...)
			break
		}
	}
	f.Fixed = fixedVersion(v, p)
	return f
}

// fixedVersion returns the closest fix newer than the locked version, so a
// project on 12.x is pointed at the 12.x fix rather than a later major.
func fixedVersion(v osvVuln, p lockedPackage) string {
	best := ""
	for _, affected := range v.Affected {
		if !strings.EqualFold(affected.Package.Name, p.Name) {
			continue
		}
		for _, r := range affected.Ranges {
			for _, event := range r.Events {
				fixed := event["fixed"]
				if fixed != "" && compareVersions(fixed, p.Version) > 0 && (best == "" || compareVersions(fixed, best) < 0) {
					best = fixed
				}
			}
		}
	}
	return best
}

func (s *Scanner) queryNVD(ctx context.Context, runtime Runtime) ([]Finding, error) {
	query := url.Values{}
	query.Set("virtualMatchString", "cpe:2.3:a:"+runtime.Product+":"+runtime.Version+":*:*:*:*:*:*:*")
	query.Set("resultsPerPage", "2000")
	var result struct {
		Vulnerabilities []struct {
			CVE struct {
				ID           string `json:"id"`
				Published    string `json:"published"`
				Descriptions []struct {
					Lang  string `json:"lang"`
					Value string `json:"value"`
				} `json:"descriptions"`
				Metrics map[string][]struct {
					CVSSData struct {
						BaseScore    float64 `json:"baseScore"`
						BaseSeverity string  `json:"baseSeverity"`
					} `json:"cvssData"`
					BaseSeverity string `json:"baseSeverity"`
				} `json:"metrics"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	if err := s.getJSON(ctx, s.NVDURL+"?"+query.Encode(), &result); err != nil {
		return nil, fmt.Errorf("nvd: %w", err)
	}
	findings := []Finding{}
	for _, item := range result.Vulnerabilities {
		c := item.CVE
		f := Finding{ID: c.ID, Component: runtime.Component, Version: runtime.Version, Severity: "UNKNOWN", URL: "https://nvd.nist.gov/vuln/detail/" + c.ID, Source: "NVD"}
		f.Published, _ = time.Parse("2006-01-02T15:04:05.000", c.Published)
		for _, d := range c.Descriptions {
			if d.Lang == "en" {
				f.Summary = firstLine(d.Value)
				break
			}
		}
		for _, key := range []string{"cvssMetricV40", "cvssMetricV31", "cvssMetricV30", "cvssMetricV2"} {
			if metrics := c.Metrics[key]; len(metrics) > 0 {
				f.Score = metrics[0].CVSSData.BaseScore
				f.Severity = normaliseSeverity(firstNonEmpty(metrics[0].CVSSData.BaseSeverity, metrics[0].BaseSeverity))
				break
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

func (s *Scanner) postJSON(ctx context.Context, endpoint string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req, out)
}

func (s *Scanner) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return s.do(req, out)
}

func (s *Scanner) do(req *http.Request, out any) error {
	req.Header.Set("User-Agent", "stacknest")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(snippet))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// readLock lists the packages in composer.lock, development ones included.
func readLock(dir string) ([]lockedPackage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "composer.lock"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Packages    []struct{ Name, Version string } `json:"packages"`
		PackagesDev []struct{ Name, Version string } `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("composer.lock: %w", err)
	}
	var packages []lockedPackage
	for _, p := range append(lock.Packages, lock.PackagesDev...) {
		// Branches such as dev-main have no published advisories to match.
		if strings.HasPrefix(p.Version, "dev-") {
			continue
		}
		packages = append(packages, lockedPackage{Name: p.Name, Version: strings.TrimPrefix(p.Version, "v")})
	}
	return packages, nil
}

func fingerprintOf(projects []Project, locks map[string][]lockedPackage, runtimes []Runtime) string {
	h := sha256.New()
	for _, p := range projects {
		fmt.Fprintf(h, "p\x00%s\x00%s\x00%s\n", p.Name, p.Domain, p.Dir)
		for _, pkg := range locks[p.Name] {
			fmt.Fprintf(h, "%s@%s\n", pkg.Name, pkg.Version)
		}
	}
	for _, r := range runtimes {
		fmt.Fprintf(h, "r\x00%s\x00%s\x00%s\n", r.Component, r.Version, strings.Join(r.Projects, ","))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func load(file string) (Report, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Report{}, err
	}
	var report Report
	err = json.Unmarshal(data, &report)
	return report, err
}

func save(file string, report Report) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Published.After(findings[j].Published) })
}

func normaliseSeverity(severity string) string {
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		return "CRITICAL"
	case "HIGH":
		return "HIGH"
	case "MODERATE", "MEDIUM":
		return "MEDIUM"
	case "LOW":
		return "LOW"
	}
	return "UNKNOWN"
}

// compareVersions compares dotted numeric versions; non-numeric parts compare as text.
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		x, y := "0", "0"
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		var nx, ny int
		_, ex := fmt.Sscanf(x, "%d", &nx)
		_, ey := fmt.Sscanf(y, "%d", &ny)
		switch {
		case ex == nil && ey == nil && nx != ny:
			if nx < ny {
				return -1
			}
			return 1
		case (ex != nil || ey != nil) && x != y:
			return strings.Compare(x, y)
		}
	}
	return 0
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 300 {
		line = line[:297] + "..."
	}
	return line
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func without(list []string, value string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}
