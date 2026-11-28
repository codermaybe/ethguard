package ipc

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDialerConsumesOnlyFrozenTargets(t *testing.T) {
	for _, target := range []string{"./socket.ipc", "../socket.ipc", "socket.ipc", "ipc:///tmp/socket.ipc", "http://localhost"} {
		tr, err := NewDialer().DialContext(t.Context(), target)
		if err == nil {
			tr.Close()
			t.Fatalf("unfrozen target accepted: %q", target)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewDialer().DialContext(ctx, filepath.Join(t.TempDir(), "socket.ipc")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled construction: %v", err)
	}
	if runtime.GOOS != "windows" {
		if _, err := NewDialer().DialContext(t.Context(), `\\.\pipe\node`); !errors.Is(err, ErrUnsupportedPlatform) {
			t.Fatalf("named pipe on %s: %v", runtime.GOOS, err)
		}
	}
}
