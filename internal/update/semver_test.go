package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.2", false},
		{"2.0.0", "1.9.9", false},
		{"v1.0.0", "v1.0.1", true},    // leading v tolerated
		{"1.0.0", "1.0.0-rc1", false}, // prerelease < release
		{"1.0.0-rc1", "1.0.0", true},  // release > prerelease
		{"dev", "1.0.0", true},        // dev build always updatable
		{"", "0.1.0", true},           // empty current always updatable
		{"1.0.0", "garbage", false},   // unparseable candidate never newer
		{"1.0.0", "1.0", false},       // 1.0 == 1.0.0
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.candidate); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.candidate, got, c.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	v := parseSemver("v2.3.4-beta1")
	if !v.ok || v.major != 2 || v.minor != 3 || v.patch != 4 || v.pre != "beta1" {
		t.Fatalf("parsed = %+v", v)
	}
	if parseSemver("not-a-version").ok {
		t.Fatal("expected parse failure for non-numeric core")
	}
}
