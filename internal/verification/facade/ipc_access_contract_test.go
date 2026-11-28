//go:build unix

package ethguard_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/core/types"
)

type unixFacadePeer struct {
	*subscriptionPeer
	connections atomic.Int32
	stop        func()
}

func shortIPCFacadeDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "eg-facade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newUnixFacadePeer(t *testing.T, path string, answer func(sourceWireRequest) string) *unixFacadePeer {
	t.Helper()
	if path == "" {
		path = filepath.Join(shortIPCFacadeDirectory(t), "node.ipc")
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	peer := &unixFacadePeer{subscriptionPeer: &subscriptionPeer{endpoint: path}}
	var mu sync.Mutex
	var connections []net.Conn
	var stopping bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			peer.connections.Add(1)
			mu.Lock()
			if stopping {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			connections = append(connections, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				write := func(body []byte) error {
					peer.writeMu.Lock()
					defer peer.writeMu.Unlock()
					_, err := conn.Write(body)
					return err
				}
				reader := bufio.NewReader(conn)
				for {
					body, err := reader.ReadBytes('\n')
					if err != nil {
						return
					}
					var request sourceWireRequest
					if err := json.Unmarshal(body, &request); err != nil {
						t.Errorf("IPC request: %v", err)
						return
					}
					peer.mu.Lock()
					peer.requests = append(peer.requests, subscriptionPeerRequest{Method: request.Method, Params: request.Params})
					peer.seq++
					seq := peer.seq
					peer.mu.Unlock()
					if request.Method == "eth_subscribe" {
						id := fmt.Sprintf("ipc-sub-%d", seq)
						peer.mu.Lock()
						queued := peer.pending
						peer.pending = nil
						peer.push = func(raw []byte) error {
							return write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, id, raw)))
						}
						peer.mu.Unlock()
						if err := write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%q}`, request.ID, id))); err != nil {
							return
						}
						for _, result := range queued {
							if err := write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, id, result))); err != nil {
								return
							}
						}
						continue
					}
					var result string
					switch request.Method {
					case "eth_chainId":
						result = `"result":"0x1"`
					case "eth_unsubscribe":
						result = `"result":true`
					default:
						result = answer(request)
					}
					if result != "" {
						if err := write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, request.ID, result))); err != nil {
							return
						}
					}
				}
			}()
		}
	}()
	peer.stop = sync.OnceFunc(func() {
		_ = listener.Close()
		mu.Lock()
		stopping = true
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	t.Cleanup(peer.stop)
	return peer
}

func TestIPCForwardedMovingReadsUseTheSharedReferencePipeline(t *testing.T) {
	for _, selector := range singleSourceSelectors() {
		for _, method := range []string{"BalanceAt", "HeaderByNumber", "BlockByNumber", "BlockNumber", "EstimateGas", "FilterLogs"} {
			if method == "BlockNumber" && selector.name != "nil" {
				continue
			}
			t.Run(selector.name+"/"+method, func(t *testing.T) {
				raw := fixtureBlock(t, selector.height)
				hash := blockHashOf(t, raw)
				peer := newUnixFacadePeer(t, "", func(request sourceWireRequest) string {
					switch request.Method {
					case "eth_getBlockByNumber", "eth_getBlockByHash":
						return `"result":` + string(raw)
					case "eth_getBalance":
						return `"result":"0x2a"`
					case "eth_estimateGas":
						return `"result":"0x5208"`
					case "eth_getLogs":
						return `"result":[]`
					default:
						t.Errorf("unexpected IPC method %s", request.Method)
						return `"error":{"code":-32601,"message":"unexpected method"}`
					}
				})
				client, err := ethguard.Dial(peer.endpoint)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.Close)
				if peer.connections.Load() != 0 {
					t.Fatal("IPC facade construction dialed eagerly")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				ctx, capture := ethguard.WithResult(ctx)
				var result ethguard.CallResult
				switch method {
				case "BalanceAt":
					var balance *big.Int
					balance, result, err = client.BalanceAt(ctx, singleSourceAccount, selector.number)
					if err == nil && (balance == nil || balance.Cmp(big.NewInt(42)) != 0) {
						t.Fatalf("balance=%v", balance)
					}
				case "HeaderByNumber":
					var header *ethapi.Header
					header, result, err = client.HeaderByNumber(ctx, selector.number)
					if err == nil && (header == nil || string(header.Hash) != hash) {
						t.Fatalf("header=%+v", header)
					}
				case "BlockByNumber":
					var block *ethapi.Block
					block, result, err = client.BlockByNumber(ctx, selector.number)
					if err == nil && (block == nil || string(block.Hash) != hash) {
						t.Fatalf("block=%+v", block)
					}
				case "BlockNumber":
					var height uint64
					height, result, err = client.BlockNumber(ctx)
					if err == nil && height != uint64(selector.height) {
						t.Fatalf("height=%d", height)
					}
				case "EstimateGas":
					msg, msgErr := ethapi.NewCallMsg(json.RawMessage(`{"to":"0x0000000000000000000000000000000000000001"}`))
					if msgErr != nil {
						t.Fatal(msgErr)
					}
					var gas uint64
					gas, result, err = client.EstimateGasAtBlock(ctx, msg, selector.number)
					if err == nil && gas != 21000 {
						t.Fatalf("gas=%d", gas)
					}
				case "FilterLogs":
					query := mustFilterQuery(t, fmt.Sprintf(`{"fromBlock":%q,"toBlock":%q}`, selector.tag, selector.tag))
					_, result, err = client.FilterLogs(ctx, query)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertSingleSourceReference(t, result, selector, hash)
				captured, done, captureErr := capture.Result()
				if !done || captureErr != nil || !reflect.DeepEqual(captured, result) {
					t.Fatalf("capture done=%v err=%v result=%+v", done, captureErr, captured)
				}
				peer.mu.Lock()
				requests := append([]subscriptionPeerRequest(nil), peer.requests...)
				peer.mu.Unlock()
				if len(requests) != 2 || requests[0].Method != "eth_getBlockByNumber" || string(requests[0].Params[0]) != fmt.Sprintf("%q", selector.tag) {
					t.Fatalf("IPC reference round=%+v", requests)
				}
				business := requests[1]
				switch method {
				case "HeaderByNumber", "BlockByNumber", "BlockNumber":
					if business.Method != "eth_getBlockByHash" || string(business.Params[0]) != fmt.Sprintf("%q", hash) {
						t.Fatalf("block read lost hash pin: %+v", business)
					}
				case "FilterLogs":
					var filter map[string]string
					if json.Unmarshal(business.Params[0], &filter) != nil || filter["fromBlock"] != fmt.Sprintf("0x%x", selector.height) || filter["toBlock"] != fmt.Sprintf("0x%x", selector.height) {
						t.Fatalf("logs lost height pin: %+v", business)
					}
				default:
					if len(business.Params) != 2 || string(business.Params[1]) != fmt.Sprintf("%q", hash) {
						t.Fatalf("read lost hash pin: %+v", business)
					}
				}
				if peer.connections.Load() != 1 || client.Mode() != rpc.CorroborationForwarded || client.VotingMembers() != 1 {
					t.Fatal("single-source IPC changed connection ownership or confidence mode")
				}
			})
		}
	}
}

func TestIPCAndWSSSubscriptionsKeepTheSameEvidenceBasis(t *testing.T) {
	for _, single := range []bool{true, false} {
		for _, view := range []bool{false, true} {
			for _, method := range []string{"heads", "logs", "receipts"} {
				t.Run(fmt.Sprintf("single=%v/view=%v/%s", single, view, method), func(t *testing.T) {
					block, receipts, hash := logsFacadeBlock(t)
					responses := map[string][]byte{"eth_chainId": []byte(`"0x1"`), "eth_getBlockByHash": block, "eth_getBlockReceipts": receipts}
					ipcPeer := newUnixFacadePeer(t, "", func(request sourceWireRequest) string {
						if raw, ok := responses[request.Method]; ok {
							return `"result":` + string(raw)
						}
						return ""
					})
					ipcPeer.queueNotification(block)
					peers := []*subscriptionPeer{ipcPeer.subscriptionPeer}
					endpoints := []string{ipcPeer.endpoint}
					if !single {
						ws := newSubscriptionPeer(t, responses)
						ws.queueNotification(block)
						peers = append(peers, ws)
						endpoints = append(endpoints, ws.endpoint)
					}
					client, err := ethguard.Dial(endpoints...)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(client.Close)
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					// This transport integration includes the first schema load in
					// a fresh race process. It does not test a one-second content
					// deadline; keep both content phases bounded with cold-start room.
					logsPolicy := logsFacadePolicy()
					logsPolicy.RequestTimeout, logsPolicy.RecoveryTimeout = 5*time.Second, 5*time.Second
					receiptsPolicy := receiptsFacadePolicy()
					receiptsPolicy.RequestTimeout, receiptsPolicy.RecoveryTimeout = 5*time.Second, 5*time.Second
					var basis ethguard.HeadEvidenceBasis
					switch method {
					case "heads":
						var sub *ethguard.HeadSubscription
						if view {
							sub, err = client.EthClient().SubscribeNewHead(ctx, logsFacadePolicy().Head)
						} else {
							sub, err = client.SubscribeNewHead(ctx, logsFacadePolicy().Head)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = sub.Close() }()
						basis = sub.Basis()
						event, err := sub.Next(ctx)
						if err != nil || len(event.Path.Appended) != 1 || string(event.Path.Appended[0].Header.Hash) != hash.Hex() {
							t.Fatalf("head event=%+v err=%v", event, err)
						}
						wantTier := ethguard.HeadCorroborated
						if single {
							wantTier = ethguard.HeadForwarded
						}
						if event.Path.Appended[0].Tier != wantTier {
							t.Fatalf("head tier=%v want=%v", event.Path.Appended[0].Tier, wantTier)
						}
						if !single {
							// A disconnected IPC member must not shrink the basis or
							// turn a later WSS-only head into corroborated progress.
							ipcPeer.stop()
							var next types.Header
							if err := json.Unmarshal(block, &next); err != nil {
								t.Fatal(err)
							}
							if next.Hash() != hash {
								t.Fatal("fixture header hash does not match the validated block")
							}
							next.ParentHash, next.Number = hash, big.NewInt(18)
							raw, err := json.Marshal(&next)
							if err != nil {
								t.Fatal(err)
							}
							ws := peers[1]
							ws.mu.Lock()
							push := ws.push
							ws.headResponse = raw
							ws.mu.Unlock()
							if err := push(raw); err != nil {
								t.Fatal(err)
							}
							wait, stop := context.WithTimeout(ctx, 100*time.Millisecond)
							defer stop()
							if event, err := sub.Next(wait); err == nil {
								t.Fatalf("one surviving source advanced strict mixed subscription: %+v", event)
							}
							if !reflect.DeepEqual(sub.Basis(), basis) {
								t.Fatal("disconnect changed the subscription evidence basis")
							}
						}
					case "logs":
						query := mustFilterQuery(t, `{"address":"0xcccccccccccccccccccccccccccccccccccccccc"}`)
						var sub *ethguard.LogsSubscription
						if view {
							sub, err = client.EthClient().SubscribeFilterLogs(ctx, query, logsPolicy)
						} else {
							sub, err = client.SubscribeFilterLogs(ctx, query, logsPolicy)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = sub.Close() }()
						basis = sub.Basis()
						event, err := sub.Next(ctx)
						if err != nil || len(event.Path.Appended) != 1 || len(event.Path.Appended[0].Logs) != 1 || event.Path.Appended[0].Proof != ethguard.LogsBlockCommitment {
							t.Fatalf("log event=%+v err=%v IPC methods=%v", event, err, ipcPeer.requestedMethods())
						}
						if event.Path.Appended[0].Node.Hash != hash {
							t.Fatal("log proof detached from witnessed block")
						}
					case "receipts":
						var sub *ethguard.ReceiptsSubscription
						if view {
							sub, err = client.EthClient().SubscribeTransactionReceipts(ctx, nil, receiptsPolicy)
						} else {
							sub, err = client.SubscribeTransactionReceipts(ctx, nil, receiptsPolicy)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = sub.Close() }()
						basis = sub.Basis()
						event, err := sub.Next(ctx)
						if err != nil || len(event.Path.Appended) != 1 || len(event.Path.Appended[0].Receipts) != 1 || event.Path.Appended[0].Proof != ethguard.ReceiptsBlockCommitment {
							t.Fatalf("receipt event=%+v err=%v", event, err)
						}
						delivery := event.Path.Appended[0]
						wantStatus := rpc.Corroborated
						if single {
							wantStatus = rpc.Forwarded
						}
						if delivery.Node.Hash != hash || delivery.ReceiptSupport.Status != wantStatus || delivery.ReceiptSupport.Basis != len(peers) || len(delivery.ReceiptSupport.Suppliers) != len(peers) {
							t.Fatalf("receipt support=%+v", delivery.ReceiptSupport)
						}
					}
					wantTier := ethguard.HeadCorroborated
					if single {
						wantTier = ethguard.HeadForwarded
					}
					if basis.Tier != wantTier || len(basis.Members) != len(peers) || basis.Quorum != len(peers) {
						t.Fatalf("fixed subscription basis=%+v", basis)
					}
					for _, peer := range peers {
						registered := false
						peer.mu.Lock()
						requests := append([]subscriptionPeerRequest(nil), peer.requests...)
						peer.mu.Unlock()
						for _, request := range requests {
							if request.Method == "eth_subscribe" {
								registered = true
								if len(request.Params) != 1 || string(request.Params[0]) != `"newHeads"` {
									t.Fatalf("unexpected subscription owner: %+v", request)
								}
							}
						}
						if !registered {
							t.Fatal("mixed basis included a peer that never subscribed")
						}
					}
				})
			}
		}
	}
}

func TestHTTPAndIPCStrictSubscriptionsRejectBeforeDial(t *testing.T) {
	http := newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":null` })
	ipcPeer := newUnixFacadePeer(t, "", func(sourceWireRequest) string { return `"result":null` })
	client, err := ethguard.Dial(http.endpoint, ipcPeer.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	for _, view := range []bool{false, true} {
		for _, method := range []string{"heads", "logs", "receipts"} {
			t.Run(fmt.Sprintf("view=%v/%s", view, method), func(t *testing.T) {
				if err := subscriptionRejectionCall(t, client, method, view, false); !errors.Is(err, ethguard.ErrInvalidRequest) {
					t.Fatalf("strict subscription with only one capable member: %v", err)
				}
			})
		}
	}
	if http.httpConnections.Load() != 0 || http.requests.Load() != 0 || ipcPeer.connections.Load() != 0 || len(ipcPeer.requestedMethods()) != 0 {
		t.Fatal("local subscription rejection probed an HTTP or IPC endpoint")
	}
	if client.Mode() != rpc.CorroborationMajority || client.VotingMembers() != 2 {
		t.Fatal("subscription filtering changed the unary voting basis")
	}
}

func TestIPCConstructorsFreezeRelativePathsBeforeDeferredDial(t *testing.T) {
	for _, entry := range []string{"Dial", "Open", "New", "LoadFile", "ParseYAML", "FromEndpoints", "URI"} {
		t.Run(entry, func(t *testing.T) {
			base, later := shortIPCFacadeDirectory(t), shortIPCFacadeDirectory(t)
			name := "geth #?% socket.ipc "
			peer := newUnixFacadePeer(t, filepath.Join(base, name), func(sourceWireRequest) string { return `"result":null` })
			t.Chdir(base)
			relative := "./" + name
			var client *ethguard.Client
			var err error
			switch entry {
			case "Dial":
				client, err = ethguard.Dial(relative)
			case "Open":
				client, err = ethguard.Open([]string{relative})
			case "New":
				input := config.Config{Upstreams: []config.Upstream{{Endpoint: relative}}}
				client, err = ethguard.New(input)
				if input.Upstreams[0].Endpoint != relative || input.Upstreams[0].Transport != "" {
					t.Fatal("New mutated caller-owned relative endpoint")
				}
			case "LoadFile":
				file := filepath.Join(later, "client.yaml")
				body := []byte(fmt.Sprintf("upstreams:\n  - endpoint: %q\n", relative))
				if err := os.WriteFile(file, body, 0o600); err != nil {
					t.Fatal(err)
				}
				client, err = ethguard.LoadFile(file)
			case "ParseYAML", "FromEndpoints":
				var cfg config.Config
				if entry == "ParseYAML" {
					cfg, err = config.ParseYAML([]byte(fmt.Sprintf("upstreams:\n  - endpoint: %q\n", relative)))
				} else {
					cfg, err = config.FromEndpoints(relative)
				}
				if err == nil {
					t.Chdir(later) // Even the later client constructor must retain the parsed target.
					client, err = ethguard.New(cfg)
				}
			case "URI":
				client, err = ethguard.Dial((&url.URL{Scheme: "ipc", Path: peer.endpoint}).String())
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			if peer.connections.Load() != 0 {
				t.Fatal("constructor opened an IPC connection")
			}
			t.Chdir(later)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id, result, err := client.ChainID(ctx)
			if err != nil || id == nil || id.Uint64() != 1 || result.Decision == nil || result.Decision.Status != rpc.Forwarded || result.IsCorroborated() {
				t.Fatalf("deferred IPC call after chdir: id=%v result=%+v err=%v", id, result, err)
			}
			if peer.connections.Load() != 1 {
				t.Fatalf("original socket connections=%d", peer.connections.Load())
			}
			if sources := client.Sources(); len(sources) != 1 || !strings.HasPrefix(sources[0].Source.ID, "ipc://") {
				t.Fatalf("IPC diagnostic identity=%+v", sources)
			}
		})
	}
}
