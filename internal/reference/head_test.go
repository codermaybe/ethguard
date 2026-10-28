package reference

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

type fakeHeadReader struct {
	observations  []HeadObservation
	confirmations []HeadObservation
}

func (f fakeHeadReader) Read(context.Context, string, time.Duration) ([]HeadObservation, error) {
	return f.observations, nil
}

func (f fakeHeadReader) Confirm(context.Context, uint64, []string, time.Duration) ([]HeadObservation, error) {
	return f.confirmations, nil
}

var errUndecodableHead = errors.New("head: missing number or hash")

// fakeHeadCodec is the test-side head contract: number and hash are the
// identity, the comparison key is the decimal height plus lowercased hash,
// and a body without an identity is a decode error. It mirrors the production
// codec's shape without binding a method policy.
type fakeHeadCodec struct{}

func (fakeHeadCodec) Decode(raw json.RawMessage) (HeadView, error) {
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return HeadView{}, err
	}
	if head.Number == "" || len(head.Hash) < 3 {
		return HeadView{}, errUndecodableHead
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	if err != nil {
		return HeadView{}, err
	}
	return HeadView{Height: height, Hash: strings.ToLower(head.Hash), Raw: raw}, nil
}

func (fakeHeadCodec) Normalize(raw json.RawMessage) (string, error) {
	view, err := fakeHeadCodec{}.Decode(raw)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(view.Height, 10) + "|" + view.Hash, nil
}

func TestFinalizedConsumesHeadReaderSeam(t *testing.T) {
	raw := json.RawMessage(`{"number":"0x10","hash":"0xabc"}`)
	reader := fakeHeadReader{observations: []HeadObservation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: raw, Attempted: true},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: raw, Attempted: true},
	}}
	ref, err := Finalized(context.Background(), reader, fakeHeadCodec{}, 32, 2, rpc.CorroborationMajority, time.Second, observe.Nop)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != rpc.Corroborated || ref.Hash != "0xabc" || ref.Height != 16 {
		t.Fatalf("reference = %+v", ref)
	}
}
