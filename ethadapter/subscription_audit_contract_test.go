package ethadapter_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethadapter"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type bridgeAuditRecord struct {
	method, phase, kind           string
	members, eventPointers        int
	appended, retracted, logCount int
	version                       uint64
	coreErr, adapterErr           error
	unaryResultPresent            bool
}

func bridgeAuditSnapshot(ev ethadapter.AuditEvent) bridgeAuditRecord {
	r := bridgeAuditRecord{method: ev.Method, coreErr: ev.CoreErr, adapterErr: ev.AdapterErr, unaryResultPresent: !reflect.DeepEqual(ev.CallResult, ethguard.CallResult{})}
	if ev.Subscription == nil {
		return r
	}
	s := ev.Subscription
	r.phase, r.members = s.Phase, len(s.Basis.Members)
	if s.Head != nil {
		r.eventPointers++
		r.kind, r.version = "heads", s.Head.Path.NewVersion
		r.appended, r.retracted = len(s.Head.Path.Appended), len(s.Head.Path.Retracted)
	}
	if s.Logs != nil {
		r.eventPointers++
		r.kind, r.version = "logs", s.Logs.Path.NewVersion
		r.appended, r.retracted = len(s.Logs.Path.Appended), len(s.Logs.Path.Retracted)
		for _, delivery := range s.Logs.Path.Appended {
			r.logCount += len(delivery.Logs)
		}
	}
	if s.Receipts != nil {
		r.eventPointers++
		r.kind, r.version = "receipts", s.Receipts.Path.NewVersion
		r.appended, r.retracted = len(s.Receipts.Path.Appended), len(s.Receipts.Path.Retracted)
	}
	return r
}

func bridgeObservedClient(t *testing.T, peer *bridgePeer, hook func(context.Context, ethadapter.AuditEvent)) *ethadapter.Client {
	t.Helper()
	core, err := ethguard.Dial(peer.url)
	if err != nil {
		t.Fatal(err)
	}
	client := ethadapter.New(core, ethadapter.WithAudit(hook))
	t.Cleanup(client.Close)
	return client
}

func TestIndependentBridgeAuditPreservesNativeEventsAndReorgDespiteMutation(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			base := bridgeFixture(t, common.Hash{}, 16, 1, 1, 1)
			first := bridgeFixture(t, base.header.Hash(), 17, 2, 1, 1)
			fork := bridgeFixture(t, base.header.Hash(), 17, 3, 1, 1)
			peer := newBridgePeer(t, base, first, fork)
			audits := make(chan bridgeAuditRecord, 8)
			client := bridgeObservedClient(t, peer, func(_ context.Context, ev ethadapter.AuditEvent) {
				audits <- bridgeAuditSnapshot(ev)
				if s := ev.Subscription; s != nil {
					if s.Head != nil {
						s.Head.Path.Retracted = nil
					}
					if s.Logs != nil {
						s.Logs.Path.Retracted = nil
					}
					if s.Receipts != nil {
						s.Receipts.Path.Retracted = nil
					}
				}
				panic("independent observer failure must not alter business conclusion")
			})
			sink := newBridgeSink(kind, 0)
			sub, err := sink.subscribe(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Unsubscribe()
			setup := bridgeAwait(t, audits)
			if setup.phase != "setup" || setup.members != 1 || setup.eventPointers != 0 || setup.unaryResultPresent || setup.coreErr != nil || setup.adapterErr != nil {
				t.Fatalf("setup audit=%+v", setup)
			}
			var previous uint64
			for _, block := range []*bridgeBlock{base, first} {
				peer.push(t, block)
				if sink.next(t) != block.header.Hash() {
					t.Fatal("observer panic changed successful delivery")
				}
				event := bridgeAwait(t, audits)
				if event.phase != "event" || event.method != setup.method || event.kind != kind || event.eventPointers != 1 || event.members != 1 || event.appended != 1 || event.retracted != 0 || event.version <= previous || event.unaryResultPresent || event.coreErr != nil || event.adapterErr != nil {
					t.Fatalf("native event audit=%+v", event)
				}
				previous = event.version
			}
			peer.push(t, fork)
			bridgeTerminal(t, sub, ethguard.ErrReorgDetected)
			reorg, terminal := bridgeAwait(t, audits), bridgeAwait(t, audits)
			if reorg.phase != "event" || reorg.kind != kind || reorg.eventPointers != 1 || reorg.retracted != 1 || reorg.version <= previous || !errors.Is(reorg.adapterErr, ethguard.ErrReorgDetected) || reorg.coreErr != nil || reorg.unaryResultPresent {
				t.Fatalf("reorg audit=%+v", reorg)
			}
			if terminal.phase != "terminal" || terminal.eventPointers != 0 || terminal.members != 1 || terminal.method != setup.method || !errors.Is(terminal.adapterErr, ethguard.ErrReorgDetected) || terminal.coreErr != nil || terminal.unaryResultPresent {
				t.Fatalf("terminal audit=%+v", terminal)
			}
			sink.assertOpenAndEmpty(t)
		})
	}
}

func TestIndependentBridgeAuditObservesZeroMatchPathAndIntentionalTerminal(t *testing.T) {
	block := bridgeFixture(t, common.Hash{}, 17, 0xf0, 2, 2)
	peer := newBridgePeer(t, block)
	audits := make(chan bridgeAuditRecord, 4)
	client := bridgeObservedClient(t, peer, func(_ context.Context, ev ethadapter.AuditEvent) { audits <- bridgeAuditSnapshot(ev) })
	sink := newBridgeSink("logs", 0)
	sink.query = ethereum.FilterQuery{Addresses: []common.Address{common.HexToAddress("0x1234")}, Topics: [][]common.Hash{{crypto.Keccak256Hash([]byte("Other(uint256)"))}}}
	sub, err := sink.subscribe(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if bridgeAwait(t, audits).phase != "setup" {
		t.Fatal("missing setup audit")
	}
	peer.push(t, block)
	event := bridgeAwait(t, audits)
	if event.phase != "event" || event.kind != "logs" || event.appended != 1 || event.logCount != 0 || event.version == 0 || event.eventPointers != 1 {
		t.Fatalf("zero-match path missing from audit: %+v", event)
	}
	sink.assertOpenAndEmpty(t)
	bridgeWaitClose(t, sub.Unsubscribe)
	bridgeTerminal(t, sub, nil)
	terminal := bridgeAwait(t, audits)
	if terminal.phase != "terminal" || terminal.coreErr != nil || terminal.adapterErr != nil || terminal.eventPointers != 0 {
		t.Fatalf("intentional terminal audit=%+v", terminal)
	}
}
