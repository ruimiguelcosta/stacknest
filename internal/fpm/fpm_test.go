package fpm

import (
	"strings"
	"testing"

	"github.com/ruimiguelcosta/stacknest/internal/config"
	"github.com/ruimiguelcosta/stacknest/internal/paths"
)

func TestINIReflectsOPcacheSettings(t *testing.T) {
	got := INI(config.OPcache{Enabled: true, Memory: 128, ValidateTimestamps: false, JIT: true})
	for _, want := range []string{"opcache.enable=1", "opcache.memory_consumption=128", "opcache.validate_timestamps=0", "opcache.jit=tracing"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFingerprintChangesWithPHPAndOPcache(t *testing.T) {
	layout := paths.Layout{Root: t.TempDir()}
	base := config.Default("8.4")
	first, err := Render(layout, "blog", base)
	if err != nil {
		t.Fatal(err)
	}
	otherPHP := base
	otherPHP.PHP = "8.5"
	second, err := Render(layout, "blog", otherPHP)
	if err != nil {
		t.Fatal(err)
	}
	otherMemory := base
	otherMemory.OPcache.Memory = 64
	third, err := Render(layout, "blog", otherMemory)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint || first.Fingerprint == third.Fingerprint {
		t.Error("fingerprint did not change")
	}
}
