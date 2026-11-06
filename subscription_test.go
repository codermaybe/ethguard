package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

type fakeSubscription struct {
	events chan transport.Notification
	errs   chan error
}

func (s *fakeSubscription) Events() <-chan transport.Notification { return s.events }
func (s *fakeSubscription) Err() <-chan error                     { return s.errs }
func (s *fakeSubscription) Unsubscribe() {
	select {
	case <-s.events:
	default:
	}
}

func TestSubscriptionFilterArgMatchesGethShape(t *testing.T) {
	arg, err := subscriptionFilterArg(ethereum.FilterQuery{Addresses: []common.Address{{1}}, Topics: [][]common.Hash{{{2}}}})
	if err != nil {
		t.Fatalf("filter arg: %v", err)
	}
	if _, ok := arg["fromBlock"]; !ok {
		t.Fatal("missing default fromBlock")
	}
	if _, ok := arg["toBlock"]; !ok {
		t.Fatal("missing default toBlock")
	}
	if _, err := subscriptionFilterArg(ethereum.FilterQuery{BlockHash: ptrHash(common.Hash{1}), FromBlock: big.NewInt(1)}); err == nil {
		t.Fatal("accepted block hash with range")
	}
}

func TestStartSubscriptionDecodesAndPropagatesErrors(t *testing.T) {
	inner := &fakeSubscription{events: make(chan transport.Notification, 1), errs: make(chan error, 1)}
	out := make(chan *struct {
		Number string `json:"number"`
	}, 1)
	sub := startSubscription(context.Background(), inner, out, func(raw []byte) (*struct {
		Number string `json:"number"`
	}, error) {
		var value struct {
			Number string `json:"number"`
		}
		return &value, json.Unmarshal(raw, &value)
	})
	inner.events <- transport.Notification{Result: json.RawMessage(`{"number":"0x2"}`)}
	select {
	case value := <-out:
		if value.Number != "0x2" {
			t.Fatalf("number = %q", value.Number)
		}
	case <-time.After(time.Second):
		t.Fatal("event not forwarded")
	}
	inner.errs <- errors.New("boom")
	select {
	case err := <-sub.Err():
		if err == nil || err.Error() != "boom" {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("error not forwarded")
	}
	sub.Unsubscribe()
}

func ptrHash(hash common.Hash) *common.Hash { return &hash }
