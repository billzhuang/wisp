package version

import "testing"

func TestCurrentDefault(t *testing.T) {
	// The default build value.
	if Current() == "" {
		t.Fatal("Current() must never be empty")
	}
}

func TestIsDev(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	for _, v := range []string{"dev", ""} {
		Version = v
		if !IsDev() {
			t.Errorf("IsDev() = false for Version=%q, want true", v)
		}
	}
	Version = "1.2.3"
	if IsDev() {
		t.Error("IsDev() = true for a real version, want false")
	}
	if Current() != "1.2.3" {
		t.Errorf("Current() = %q", Current())
	}
}

func TestRepoFormat(t *testing.T) {
	if Repo == "" {
		t.Fatal("Repo must be set for the updater")
	}
}
