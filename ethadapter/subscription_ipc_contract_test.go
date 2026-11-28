//go:build unix

package ethadapter_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

type bridgeIPCConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bridgeIPCConn) ReadMessage() ([]byte, error) { return c.reader.ReadBytes('\n') }
func (c *bridgeIPCConn) WriteText(raw []byte) error {
	n, err := c.Write(append(append([]byte(nil), raw...), '\n'))
	if err == nil && n != len(raw)+1 {
		return io.ErrShortWrite
	}
	return err
}

func newBridgeIPCPeer(t *testing.T, blocks ...*bridgeBlock) *bridgePeer {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "eg-adapter-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := &bridgePeer{connections: make(map[bridgeWireConn]bool), subs: make(map[string]bridgeWireConn), blocks: make(map[common.Hash]*bridgeBlock), changed: make(chan struct{}, 1), stopped: make(chan struct{})}
	for _, block := range blocks {
		p.blocks[block.header.Hash()] = block
	}
	p.url = filepath.Join(dir, "node.ipc")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", p.url)
	if err != nil {
		t.Fatal(err)
	}
	p.stopListener = func() { _ = listener.Close() }
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.serve(t, context.Background(), &bridgeIPCConn{Conn: conn, reader: bufio.NewReader(conn)})
			}()
		}
	}()
	t.Cleanup(p.stop)
	return p
}

func TestIndependentIPCAndMixedBridgeUseDefaultNativePipeline(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		for _, mixed := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "ipc", true: "wss_ipc"}[mixed], func(t *testing.T) {
				base := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
				next := bridgeFixture(t, base.header.Hash(), 18, 2, 1, 1)
				ipc := newBridgeIPCPeer(t, base, next)
				peers := []*bridgePeer{ipc}
				if mixed {
					peers = append(peers, newBridgePeer(t, base, next))
				}
				client := bridgeClient(t, peers...)
				sink := newBridgeSink(kind, 0)
				sub, err := sink.subscribe(t.Context(), client)
				if err != nil {
					t.Fatal(err)
				}
				defer sub.Unsubscribe()
				for _, block := range []*bridgeBlock{base, next} {
					for _, peer := range peers {
						peer.push(t, block)
					}
					if sink.next(t) != block.header.Hash() {
						t.Fatal("IPC/mixed bridge lost canonical path")
					}
				}
				bridgeWaitClose(t, client.Core().Close)
				bridgeTerminal(t, sub, nil)
				sink.assertOpenAndEmpty(t)
				for _, peer := range peers {
					peer.mu.Lock()
					requests := append([]bridgeRequest(nil), peer.requests...)
					peer.mu.Unlock()
					for _, r := range requests {
						if r.Method == "eth_subscribe" {
							var name string
							if len(r.Params) != 1 || json.Unmarshal(r.Params[0], &name) != nil || name != "newHeads" {
								t.Fatal("adapter bypassed native subscription owner")
							}
						}
					}
				}
			})
		}
	}
}
