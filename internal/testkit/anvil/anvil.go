//go:build anvil

// Package anvil owns a pinned, local-only Anvil process for opt-in integration tests.
package anvil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	Version     = "1.7.1"
	Commit      = "4072e48705af9d93e3c0f6e29e93b5e9a40caed8"
	ChainID     = 31337
	GenesisTime = 1700000000
	GasLimit    = 30000000
	GasPrice    = 2000000000
	BaseFee     = 1000000000
	Hardfork    = "shanghai"
	// These are public, unforked development accounts, never production secrets.
	Mnemonic       = "test test test test test test test test test test test junk"
	PrivateKey     = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	Sender         = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	Recipient      = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	InitialBalance = "10000000000000000000000"
	maxLogs        = 64 << 10
	maxResponse    = 2 << 20
)

type Provenance struct {
	Executable         string   `json:"executable,omitempty"`
	SHA256             string   `json:"sha256,omitempty"`
	VersionOutput      string   `json:"version_output,omitempty"`
	Arguments          []string `json:"arguments,omitempty"`
	ChainID            uint64   `json:"chain_id"`
	Hardfork           string   `json:"hardfork"`
	GenesisTime        uint64   `json:"genesis_time"`
	GasLimit           uint64   `json:"gas_limit"`
	Sender             string   `json:"sender"`
	Recipient          string   `json:"recipient"`
	IndependentSources int      `json:"independent_sources"`
}

// Record preserves original request/response bytes, including RPC errors. Anvil
// is one local oracle, not independent provider corroboration.
type Record struct {
	Request        []byte `json:"request_bytes"`
	Response       []byte `json:"response_bytes,omitempty"`
	RequestSHA256  string `json:"request_sha256"`
	ResponseSHA256 string `json:"response_sha256,omitempty"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
	Error          string `json:"error,omitempty"`
}

type Node struct {
	HTTP          string
	WS            string
	Provenance    Provenance
	cmd           *exec.Cmd
	done          chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once
	closeErr      error
	waitErr       error
	logs          boundedBuffer
	mu            sync.Mutex
	records       []Record
	recordedBytes int
	client        *http.Client
}

type boundedBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := maxLogs - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := string(b.data)
	if b.truncated {
		s += "\n[logs truncated at 64 KiB]"
	}
	// Do not retain signing material even if an error prints command arguments.
	s = strings.ReplaceAll(s, Mnemonic, "[public dev mnemonic redacted]")
	return strings.ReplaceAll(s, PrivateKey, "[public dev key redacted]")
}

// Start fails (never skips) for a missing or unpinned executable. ANVIL_BIN may
// select an installation. Every started child is reaped, including readiness
// failure and caller cancellation. The OS chooses the loopback port; releasing
// the reservation is necessary for Anvil to bind, so a bind race fails startup.
func Start(ctx context.Context) (*Node, error) {
	n := &Node{closed: make(chan struct{}), client: &http.Client{Timeout: 3 * time.Second}, Provenance: Provenance{
		ChainID: ChainID, Hardfork: Hardfork, GenesisTime: GenesisTime, GasLimit: GasLimit,
		Sender: Sender, Recipient: Recipient, IndependentSources: 1,
	}}
	binary := os.Getenv("ANVIL_BIN")
	if binary == "" {
		binary = "anvil"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return n, fmt.Errorf("required pinned Anvil: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return n, err
	}
	n.Provenance.Executable = path
	file, err := os.Open(path)
	if err != nil {
		return n, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return n, err
	}
	n.Provenance.SHA256 = hex.EncodeToString(hash.Sum(nil))
	versionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	version := exec.CommandContext(versionCtx, path, "--version")
	version.Env = []string{"NO_COLOR=1", "RUST_BACKTRACE=0"}
	version.WaitDelay = time.Second
	var output boundedBuffer
	version.Stdout, version.Stderr = &output, &output
	err = version.Run()
	cancel()
	n.Provenance.VersionOutput = output.String()
	if err != nil {
		return n, fmt.Errorf("Anvil identity: %w: %s", err, output.String())
	}
	if !regexp.MustCompile(`(?m)^anvil Version: 1\.7\.1(?:-stable)?\s*$`).MatchString(output.String()) ||
		!regexp.MustCompile(`(?m)^Commit SHA: `+Commit+`\s*$`).MatchString(output.String()) {
		return n, fmt.Errorf("required Anvil %s commit %s, got %q", Version, Commit, output.String())
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return n, err
	}
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		_ = listener.Close()
		return n, err
	}
	if err := listener.Close(); err != nil {
		return n, err
	}
	n.HTTP, n.WS = "http://"+address, "ws://"+address
	args := []string{"--host", "127.0.0.1", "--port", port, "--chain-id", "31337", "--hardfork", Hardfork,
		"--timestamp", "1700000000", "--gas-limit", "30000000", "--gas-price", "2000000000",
		"--base-fee", "1000000000", "--mnemonic", Mnemonic, "--derivation-path", "m/44'/60'/0'/0/",
		"--accounts", "2", "--balance", "10000", "--no-mining", "--quiet"}
	n.Provenance.Arguments = append([]string(nil), args...)
	for i := range n.Provenance.Arguments {
		if n.Provenance.Arguments[i] == Mnemonic {
			n.Provenance.Arguments[i] = "[public dev mnemonic redacted]"
		}
	}
	cmd := exec.Command(path, args...)
	// Do not inherit ANVIL_FORK_URL, RPC URLs, credentials, proxy settings, or
	// any other user environment capable of changing this unforked chain.
	cmd.Env = []string{"NO_COLOR=1", "RUST_BACKTRACE=0"}
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = &n.logs, &n.logs
	if err := cmd.Start(); err != nil {
		return n, fmt.Errorf("starting Anvil: %w", err)
	}
	n.cmd, n.done = cmd, make(chan struct{})
	go func() { n.waitErr = cmd.Wait(); close(n.done) }()
	go func() {
		select {
		case <-ctx.Done():
			_ = n.Close()
		case <-n.closed:
		}
	}()
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readyCancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		var chain string
		if lastErr = n.Call(readyCtx, &chain, "eth_chainId"); lastErr == nil {
			if chain != "0x7a69" {
				_ = n.Close()
				return n, fmt.Errorf("unexpected chain ID %s", chain)
			}
			return n, nil
		}
		select {
		case <-n.done:
			_ = n.Close()
			return n, fmt.Errorf("Anvil exited before readiness: %v; %s", n.waitErr, n.Logs())
		case <-readyCtx.Done():
			_ = n.Close()
			return n, fmt.Errorf("Anvil readiness: %w (last RPC: %v); %s", readyCtx.Err(), lastErr, n.Logs())
		case <-ticker.C:
		}
	}
}

// Close first asks Anvil to exit, then kills after a bounded grace period and
// waits again. It is idempotent and safe alongside context cancellation.
func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		defer close(n.closed)
		n.client.CloseIdleConnections()
		if n.cmd == nil {
			return
		}
		select {
		case <-n.done:
			return
		default:
		}
		if err := n.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			n.closeErr = err
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-n.done:
			return
		case <-timer.C:
		}
		if err := n.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			n.closeErr = errors.Join(n.closeErr, err)
		}
		timer.Reset(2 * time.Second)
		select {
		case <-n.done:
		case <-timer.C:
			n.closeErr = errors.Join(n.closeErr, errors.New("Anvil did not reap after kill"))
		}
	})
	return n.closeErr
}

func (n *Node) Logs() string { return n.logs.String() }

func (n *Node) Records() []Record {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Record(nil), n.records...)
}

// Call is only for test controls and the direct state oracle. Public consumer
// reads must use EthGuard rather than this client. Each request is bounded.
func (n *Node) Call(ctx context.Context, result any, method string, params ...any) (err error) {
	if params == nil {
		params = []any{}
	}
	request, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
	}{"2.0", 1, method, params})
	if err != nil {
		return err
	}
	requestDigest := sha256.Sum256(request)
	record := Record{Request: request, RequestSHA256: hex.EncodeToString(requestDigest[:])}
	defer func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		remaining := (8 << 20) - n.recordedBytes - len(request)
		if remaining < 0 {
			remaining = 0
		}
		if len(record.Response) > remaining {
			record.Response = record.Response[:remaining]
			record.Truncated = true
			err = errors.Join(err, errors.New("Anvil raw evidence exceeds 8 MiB retention bound"))
		}
		if err != nil {
			record.Error = err.Error()
		}
		n.recordedBytes += len(record.Request) + len(record.Response)
		n.records = append(n.records, record)
	}()
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, n.HTTP, bytes.NewReader(request))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	record.HTTPStatus = response.StatusCode
	record.Response = body
	responseDigest := sha256.Sum256(body)
	record.ResponseSHA256 = hex.EncodeToString(responseDigest[:])
	if err != nil {
		return err
	}
	if len(body) > maxResponse {
		record.Truncated = true
		return fmt.Errorf("Anvil response exceeds %d bytes", maxResponse)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Anvil HTTP status %d", response.StatusCode)
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != 1 {
		return errors.New("invalid Anvil RPC envelope")
	}
	if len(envelope.Error) != 0 && string(envelope.Error) != "null" {
		return fmt.Errorf("Anvil RPC %s: %s", method, envelope.Error)
	}
	if len(envelope.Result) == 0 {
		return errors.New("missing Anvil RPC result")
	}
	return json.Unmarshal(envelope.Result, result)
}
