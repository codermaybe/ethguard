package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

type sampleTransport struct {
	call func(context.Context, string, ...any) rpc.Response
}

func (s *sampleTransport) Call(ctx context.Context, method string, params ...any) rpc.Response {
	return s.call(ctx, method, params...)
}
func (*sampleTransport) ChainID(context.Context) (*big.Int, error) { return big.NewInt(1), nil }
func (*sampleTransport) Close()                                    {}

type sampleDialer struct{ transport transport.Transport }

func (d sampleDialer) DialContext(context.Context, string) (transport.Transport, error) {
	return d.transport, nil
}

func sampleClient(t *testing.T, timeout time.Duration, callbacks ...func(context.Context, string, ...any) rpc.Response) *Client {
	t.Helper()
	members := make([]pool.Member, len(callbacks))
	for i, callback := range callbacks {
		members[i] = pool.Member{Name: fmt.Sprintf("source-%d", i), URL: fmt.Sprintf("https://source-%d.test", i), Dialer: sampleDialer{&sampleTransport{call: callback}}}
	}
	p, err := pool.NewWithMembers(1, members)
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancelCause(context.Background())
	c := &Client{pool: p, collectTimeout: timeout, lifeCtx: life, cancelLife: cancel}
	t.Cleanup(c.Close)
	return c
}

func sampleResult(raw json.RawMessage) rpc.Response {
	return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}
}

type sampleChangingParam struct{ calls atomic.Int32 }

func (p *sampleChangingParam) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"revision":%d}`, p.calls.Add(1)), nil
}

func TestSampleOwnsFullResponsesAndOneFrozenRequest(t *testing.T) {
	large := json.RawMessage(`"` + strings.Repeat("a", 128<<10) + `"`)
	calls := make(chan []any, 2)
	release := make(chan struct{})
	callback := func(ctx context.Context, method string, params ...any) rpc.Response {
		if method != "eth_getBalance" {
			t.Errorf("unexpected reference or secondary request: %s", method)
		}
		calls <- params
		<-release
		return sampleResult(large)
	}
	client := sampleClient(t, time.Second, callback, callback)
	mutable := map[string]any{"address": "original"}
	changing := &sampleChangingParam{}
	type completed struct {
		observations []rpc.Observation
		err          error
	}
	done := make(chan completed, 1)
	go func() {
		observations, err := client.Sample(t.Context(), "eth_getBalance", mutable, "latest", changing)
		done <- completed{observations, err}
	}()
	first, second := <-calls, <-calls
	mutable["address"] = "changed"
	for _, params := range [][]any{first, second} {
		if string(params[0].(json.RawMessage)) != `{"address":"original"}` || string(params[1].(json.RawMessage)) != `"latest"` || string(params[2].(json.RawMessage)) != `{"revision":1}` {
			t.Errorf("diagnostic parameters were rewritten or re-serialized: %v", params)
		}
	}
	close(release)
	result := <-done
	if result.err != nil || len(result.observations) != 2 || changing.calls.Load() != 1 {
		t.Fatalf("sample=%+v marshal calls=%d", result, changing.calls.Load())
	}
	for i, observation := range result.observations {
		if !observation.Attempted || observation.Source.Name != fmt.Sprintf("source-%d", i) || !bytes.Equal(observation.Response.Answer.Result, large) || len(observation.Evidence) != 0 {
			t.Fatalf("source %d lost complete unjudged response: attempted=%v size=%d evidence=%s", i, observation.Attempted, len(observation.Response.Answer.Result), observation.Evidence)
		}
	}
	result.observations[0].Response.Answer.Result[1] = 'z'
	if result.observations[1].Response.Answer.Result[1] != 'a' || large[1] != 'a' {
		t.Fatal("sample responses share mutable bytes")
	}
	sources := client.Sources()
	for _, source := range sources {
		if source.Health.Successes != 1 || source.Health.Failures != 0 {
			t.Fatalf("sample health=%+v", source.Health)
		}
	}
	sources[0].Source.Name = "mutated"
	sources[0].Health.Successes = 99
	again := client.Sources()
	if again[0].Source.Name != "source-0" || again[0].Health.Successes != 1 {
		t.Fatal("source snapshots share mutable state")
	}
}

type sampleProtocolCause struct{}

func (*sampleProtocolCause) Error() string  { return "execution reverted" }
func (*sampleProtocolCause) ErrorCode() int { return 3 }

func TestSamplePreservesProtocolDataAndCollectionErrors(t *testing.T) {
	cause := &sampleProtocolCause{}
	data := json.RawMessage(`"` + strings.Repeat("b", 96<<10) + `"`)
	body := &rpc.RPCError{Code: 3, Message: "execution reverted", Data: data, DataPresence: rpc.ErrorDataPresenceValue}
	protocol := rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerProtocolError, Cause: cause, RPCError: body}}
	c := sampleClient(t, time.Second, func(context.Context, string, ...any) rpc.Response { return protocol }, func(context.Context, string, ...any) rpc.Response { return protocol })
	observations, err := c.Sample(t.Context(), "eth_call")
	if err != nil || len(observations) != 2 {
		t.Fatalf("protocol sample=%+v err=%v", observations, err)
	}
	for _, observation := range observations {
		if !errors.Is(observation.Response.Err(), cause) || !bytes.Equal(observation.Response.Answer.RPCError.Data, data) {
			t.Fatal("protocol response facts were lost")
		}
	}
	observations[0].Response.Answer.RPCError.Data[1] = 'z'
	observations[0].Response.Answer.RPCError.Message = "changed"
	if body.Message != "execution reverted" || data[1] != 'b' || observations[1].Response.Answer.RPCError.Data[1] != 'b' {
		t.Fatal("protocol snapshots share mutable storage")
	}
	failure := errors.New("source offline")
	down := sampleClient(t, time.Second, func(context.Context, string, ...any) rpc.Response {
		return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: failure}}
	})
	failed, err := down.Sample(t.Context(), "debug_raw")
	if !errors.Is(err, ErrAllSourcesDown) || len(failed) != 1 || !failed[0].Attempted || !errors.Is(failed[0].Response.Err(), failure) {
		t.Fatalf("collection error discarded source evidence: %+v %v", failed, err)
	}
	if h := down.Sources()[0].Health; h.Failures != 1 || h.Successes != 0 {
		t.Fatalf("failed sample health=%+v", h)
	}
}

func TestSampleLifecycleAndTimeout(t *testing.T) {
	for _, mode := range []string{"owner close", "caller cancellation", "caller deadline", "collection deadline", "closed", "canceled before dispatch"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			timeout := time.Second
			if mode == "collection deadline" {
				timeout = 25 * time.Millisecond
			}
			c := sampleClient(t, timeout, func(ctx context.Context, _ string, _ ...any) rpc.Response {
				started <- struct{}{}
				<-ctx.Done()
				return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: ctx.Err()}}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "caller deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 10*time.Millisecond)
				defer stop()
			}
			if mode == "closed" {
				c.Close()
			}
			if mode == "canceled before dispatch" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { _, err := c.Sample(ctx, "debug_block"); done <- err }()
			if mode == "owner close" || mode == "caller cancellation" {
				<-started
				if mode == "owner close" {
					c.Close()
				} else {
					cancel()
				}
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("sample did not converge")
			}
			switch mode {
			case "closed", "owner close":
				if !errors.Is(err, ErrPoolClosed) {
					t.Fatalf("close error=%v", err)
				}
			case "caller cancellation", "canceled before dispatch":
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrPoolClosed) {
					t.Fatalf("caller cancellation=%v", err)
				}
			default:
				if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPoolClosed) {
					t.Fatalf("deadline=%v", err)
				}
			}
			if mode == "closed" || mode == "canceled before dispatch" {
				select {
				case <-started:
					t.Fatal("rejected sample reached source")
				default:
				}
			}
			h := c.Sources()[0].Health
			if mode != "collection deadline" && (h.Successes != 0 || h.Failures != 0) {
				t.Fatalf("caller lifecycle changed health: %+v", h)
			}
		})
	}
}

func TestSampleUsableResponseKeepsCollectDeadlinePriority(t *testing.T) {
	c := sampleClient(t, 20*time.Millisecond,
		func(context.Context, string, ...any) rpc.Response { return sampleResult(json.RawMessage(`"ok"`)) },
		func(ctx context.Context, _ string, _ ...any) rpc.Response {
			<-ctx.Done()
			return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: ctx.Err()}}
		})
	observations, err := c.Sample(t.Context(), "debug_partial")
	if err != nil || len(observations) != 2 || string(observations[0].Response.Answer.Result) != `"ok"` || !errors.Is(observations[1].Response.Err(), context.DeadlineExceeded) {
		t.Fatalf("Collect priority changed: %+v %v", observations, err)
	}
}

func TestSampleInvalidParamsFailBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	c := sampleClient(t, time.Second, func(context.Context, string, ...any) rpc.Response {
		calls.Add(1)
		return sampleResult(json.RawMessage(`null`))
	})
	if observations, err := c.Sample(t.Context(), "debug_raw", make(chan int)); !errors.Is(err, rpc.ErrInvalidRequest) || len(observations) != 0 || calls.Load() != 0 {
		t.Fatalf("invalid params reached source: %+v %v", observations, err)
	}
}

func TestClientDiagnosticsExposeValuesWithoutPoolCapabilities(t *testing.T) {
	client := reflect.TypeFor[*Client]()
	if _, ok := client.MethodByName("Pool"); ok {
		t.Fatal("Client still exposes the pool")
	}
	if reflect.TypeFor[observe.HealthSnapshot]().PkgPath() != "github.com/codermaybe/ethguard/observe" {
		t.Fatal("health snapshot is an alias to its former owner")
	}
	seen := map[reflect.Type]bool{}
	var inspect func(reflect.Type)
	inspect = func(typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		if strings.HasPrefix(typ.PkgPath(), "github.com/codermaybe/ethguard/internal/pool") {
			t.Errorf("public signature exposes %v", typ)
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			inspect(typ.Elem())
		case reflect.Map:
			inspect(typ.Key())
			inspect(typ.Elem())
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.IsExported() {
					inspect(field.Type)
				}
			}
		case reflect.Func:
			for i := 0; i < typ.NumIn(); i++ {
				inspect(typ.In(i))
			}
			for i := 0; i < typ.NumOut(); i++ {
				inspect(typ.Out(i))
			}
		}
	}
	for i := 0; i < client.NumMethod(); i++ {
		inspect(client.Method(i).Type)
	}
	inspect(reflect.TypeFor[SourceSnapshot]())
}
