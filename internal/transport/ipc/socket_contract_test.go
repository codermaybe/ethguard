//go:build unix

package ipc_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/codermaybe/ethguard/internal/transport/ipc"
)

func unixIPCContractListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	// Keep below sockaddr_un's platform path bound, independently of the
	// descriptive test name and testing.T.TempDir's long generated prefix.
	dir, err := os.MkdirTemp("/tmp", "eg-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "node.ipc")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener, path
}

func TestUnixSocketConcurrentResponsesAndProtocolPresence(t *testing.T) {
	listener, target := unixIPCContractListener(t)
	runIPCConcurrentResponsesAndProtocolPresence(t, listener, target)
}

func TestUnixSocketCancellationAndGracefulClose(t *testing.T) {
	listener, target := unixIPCContractListener(t)
	runIPCCancelAndGracefulClose(t, listener, target)
}

func TestUnixSocketPrebindSubscriptionSharesUnaryConnection(t *testing.T) {
	listener, target := unixIPCContractListener(t)
	runIPCPrebindSubscriptionAndUnarySharing(t, listener, target)
}

func TestUnixSocketDialHonorsCanceledContext(t *testing.T) {
	listener, target := unixIPCContractListener(t)
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if tr, err := ipc.NewDialer().DialContext(ctx, target); !errors.Is(err, context.Canceled) || tr != nil {
		if tr != nil {
			tr.Close()
		}
		t.Fatalf("canceled IPC dial: transport=%v err=%v", tr, err)
	}
}
