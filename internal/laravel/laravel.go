// Package laravel finds installable Laravel releases on Packagist.
package laravel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"

	"github.com/Masterminds/semver/v3"
)

const (
	packagistURL = "https://repo.packagist.org/p2/laravel/framework.json"
	packageName  = "laravel/framework"
	// OfferedMajors is the current major plus the two before it.
	OfferedMajors = 3
)

// SupportedPHP lists the PHP versions devstack can run, newest first.
var SupportedPHP = []string{"8.5", "8.4", "8.3", "8.2", "8.1"}

var stableVersion = regexp.MustCompile(`^v?(\d+)\.\d+\.\d+$`)
var constraintOr = regexp.MustCompile(`\|+`)

type Release struct {
	Major         int
	Version       string
	PHPConstraint string
}

// Fetch returns the newest OfferedMajors majors from Packagist, newest first.
func Fetch(ctx context.Context, client *http.Client) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, packagistURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "devstack")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("packagist returned %s", resp.Status)
	}
	return Parse(resp.Body, OfferedMajors)
}

// Parse reads Packagist's minified format, where each entry only lists the
// fields that changed from the previous one and "__unset" removes a field.
// Entries are ordered newest first, so the first stable tag of a major is its latest.
func Parse(r io.Reader, majors int) ([]Release, error) {
	var doc struct {
		Packages map[string][]map[string]any `json:"packages"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode packagist response: %w", err)
	}

	current := map[string]any{}
	byMajor := map[int]Release{}
	for _, entry := range doc.Packages[packageName] {
		for key, value := range entry {
			if value == "__unset" {
				delete(current, key)
			} else {
				current[key] = value
			}
		}
		version, _ := current["version"].(string)
		match := stableVersion.FindStringSubmatch(version)
		if match == nil {
			continue
		}
		major, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if _, seen := byMajor[major]; seen {
			continue
		}
		require, _ := current["require"].(map[string]any)
		php, _ := require["php"].(string)
		byMajor[major] = Release{Major: major, Version: version, PHPConstraint: php}
	}
	if len(byMajor) == 0 {
		return nil, fmt.Errorf("no stable releases of %s found", packageName)
	}

	releases := make([]Release, 0, len(byMajor))
	for _, release := range byMajor {
		releases = append(releases, release)
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].Major > releases[j].Major })
	if len(releases) > majors {
		releases = releases[:majors]
	}
	return releases, nil
}

// ChoosePHP returns the first candidate that satisfies the constraint.
func ChoosePHP(constraint string, candidates []string) (string, error) {
	parsed, err := semver.NewConstraint(constraintOr.ReplaceAllString(constraint, "||"))
	if err != nil {
		return "", fmt.Errorf("invalid PHP constraint %q: %w", constraint, err)
	}
	for _, candidate := range candidates {
		version, err := semver.NewVersion(candidate + ".0")
		if err != nil {
			return "", fmt.Errorf("invalid PHP version %q: %w", candidate, err)
		}
		if parsed.Check(version) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no PHP version among %v satisfies %q", candidates, constraint)
}

// CreateProjectArgs returns the composer arguments that scaffold a project.
func CreateProjectArgs(major int, dir string) []string {
	return []string{
		"create-project", "--prefer-dist", "--no-interaction",
		fmt.Sprintf("laravel/laravel:^%d.0", major), dir,
	}
}
