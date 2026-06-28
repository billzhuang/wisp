package app_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/localpty"
)

// shellController returns a started controller running `sh -c cmd` on a PTY.
func shellController(t *testing.T, cmd string, cols, rows int) *app.Controller {
	t.Helper()
	ctrl := app.NewLocal(localpty.Config{
		Path: "/bin/sh",
		Args: []string{"/bin/sh", "-c", cmd},
		Cols: cols, Rows: rows,
	})
	if err := ctrl.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { ctrl.Close() })
	return ctrl
}

func TestControllerRunsCommandAndWait(t *testing.T) {
	ctrl := shellController(t, "printf done", 40, 10)
	out, err := io.ReadAll(ctrl.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !strings.Contains(string(out), "done") {
		t.Fatalf("stdout = %q, want it to contain %q", out, "done")
	}
	if err := ctrl.Wait(); err != nil {
		t.Fatalf("wait (clean exit expected): %v", err)
	}
}

func TestControllerResizeDoesNotError(t *testing.T) {
	ctrl := shellController(t, "sleep 1", 80, 24)
	if err := ctrl.Resize(100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
}

func TestControllerInputReachesShell(t *testing.T) {
	ctrl := shellController(t, "cat", 40, 10)
	if err := ctrl.Input([]byte("hi\n")); err != nil {
		t.Fatalf("input: %v", err)
	}

	buf := make([]byte, 64)
	var got []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, _ := ctrl.Stdout().Read(buf)
		got = append(got, buf[:n]...)
		if strings.Contains(string(got), "hi") {
			return
		}
	}
	t.Fatalf("shell never echoed input; got %q", got)
}

// TestControllerStartFailure verifies Start propagates an error for a missing
// command path.
func TestControllerStartFailure(t *testing.T) {
	ctrl := app.NewLocal(localpty.Config{Path: "/nonexistent/shell-xyz"})
	if err := ctrl.Start(); err == nil {
		ctrl.Close()
		t.Fatal("expected error starting a nonexistent shell")
	}
}

// TestControllerClose verifies Close is safe (and idempotent) on a live session.
func TestControllerClose(t *testing.T) {
	ctrl := shellController(t, "sleep 5", 40, 10)
	if err := ctrl.Close(); err != nil {
		t.Fatalf("Close returned: %v", err)
	}
	if err := ctrl.Close(); err != nil {
		t.Fatalf("second Close returned: %v", err)
	}
}
