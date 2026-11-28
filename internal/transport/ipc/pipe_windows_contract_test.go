//go:build windows

package ipc_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/Microsoft/go-winio"
	"github.com/codermaybe/ethguard/internal/transport/ipc"
)

var windowsPipeSequence atomic.Uint64

func windowsIPCContractListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	target := fmt.Sprintf(`\\.\pipe\ethguard-contract-%d-%d`, os.Getpid(), windowsPipeSequence.Add(1))
	listener, err := winio.ListenPipe(target, &winio.PipeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener, target
}

func TestWindowsNamedPipeConcurrentResponsesAndProtocolPresence(t *testing.T) {
	listener, target := windowsIPCContractListener(t)
	runIPCConcurrentResponsesAndProtocolPresence(t, listener, target)
}

func TestWindowsNamedPipeCancellationAndGracefulClose(t *testing.T) {
	listener, target := windowsIPCContractListener(t)
	runIPCCancelAndGracefulClose(t, listener, target)
}

func TestWindowsNamedPipePrebindSubscriptionSharesUnaryConnection(t *testing.T) {
	listener, target := windowsIPCContractListener(t)
	runIPCPrebindSubscriptionAndUnarySharing(t, listener, target)
}

func TestWindowsNamedPipeDialHonorsCanceledContext(t *testing.T) {
	_, target := windowsIPCContractListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if tr, err := ipc.NewDialer().DialContext(ctx, target); !errors.Is(err, context.Canceled) || tr != nil {
		if tr != nil {
			tr.Close()
		}
		t.Fatalf("canceled named pipe dial: transport=%v err=%v", tr, err)
	}
}
