//go:build ebiten

package render

import (
	"errors"
	"testing"
	"time"
)

// These tests exercise the GUI update-banner state machine without starting an
// Ebitengine game loop (no RunGame / no graphics calls), so they run headlessly
// in CI under the `ebiten` tag.

func TestSetUpdateArmsBanner(t *testing.T) {
	f := &ebitenFrontend{}
	var _ UpdatePrompter = f // compile-time: GUI frontend implements the seam

	f.SetUpdate("Update 1.2.3 available", func() error { return nil })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.banner != "Update 1.2.3 available" {
		t.Fatalf("banner = %q", f.banner)
	}
	if f.install == nil {
		t.Fatal("install action not armed")
	}
}

func TestTriggerInstallSuccess(t *testing.T) {
	f := &ebitenFrontend{}
	called := make(chan struct{}, 1)
	f.SetUpdate("Update available", func() error {
		called <- struct{}{}
		return nil
	})
	f.triggerInstall()

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("install action was not invoked")
	}
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.banner == "Update installed — restart wisp to apply" && f.install == nil && !f.updating
	})
}

func TestTriggerInstallFailureShowsError(t *testing.T) {
	f := &ebitenFrontend{}
	f.SetUpdate("Update available", func() error { return errors.New("boom") })
	f.triggerInstall()
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.banner == "Update failed: boom" && !f.updating
	})
}

func TestTriggerInstallWithoutActionIsNoop(t *testing.T) {
	f := &ebitenFrontend{}
	f.triggerInstall() // no install armed; must not panic or change state
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.banner != "" || f.updating {
		t.Fatalf("unexpected state: banner=%q updating=%v", f.banner, f.updating)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
