package laravel

import (
	"strings"
	"testing"
)

const fixture = `{"minified":"composer/2.0","packages":{"laravel/framework":[
 {"version":"v14.0.0-beta1","require":{"php":"^8.5"}},
 {"version":"v13.2.0","require":{"php":"^8.3"}},
 {"version":"v13.1.0"},
 {"version":"v12.9.0","require":{"php":"^8.2"}},
 {"version":"v11.8.0"},
 {"version":"v10.7.0","require":"__unset"}
]}}`

func TestParseKeepsLatestStableOfNewestMajors(t *testing.T) {
	got, err := Parse(strings.NewReader(fixture), OfferedMajors)
	if err != nil {
		t.Fatal(err)
	}
	want := []Release{
		{13, "v13.2.0", "^8.3"},
		{12, "v12.9.0", "^8.2"},
		{11, "v11.8.0", "^8.2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("release %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestChoosePHP(t *testing.T) {
	cases := []struct {
		constraint string
		candidates []string
		want       string
		wantErr    bool
	}{
		{"^8.3", SupportedPHP, "8.5", false},
		{"^8.1|^8.2", []string{"8.2", "8.1"}, "8.2", false},
		{"~8.2.0", SupportedPHP, "8.2", false},
		{"^8.3", []string{"8.2"}, "", true},
	}
	for _, c := range cases {
		got, err := ChoosePHP(c.constraint, c.candidates)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("ChoosePHP(%q) = %q, %v; want %q", c.constraint, got, err, c.want)
		}
	}
}

func TestCreateProjectArgsPinsMajor(t *testing.T) {
	got := strings.Join(CreateProjectArgs(12, "blog"), " ")
	if !strings.Contains(got, "laravel/laravel:^12.0 blog") {
		t.Errorf("unexpected args: %s", got)
	}
}
