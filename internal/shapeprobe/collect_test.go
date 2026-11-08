package shapeprobe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/chainlist"
)

func TestFilterPublicHTTPSAndMinHeight(t *testing.T) {
	got := FilterPublicHTTPS([]chainlist.Endpoint{
		{URL: "http://insecure.example"},
		{URL: "https://user:pass@secret.example"},
		{URL: "https://tmpl.example/${KEY}"},
		{URL: "https://b.example/eth"},
		{URL: "https://b.example/eth"},
		{URL: "https://a.example"},
	})
	if len(got) != 2 || got[0].URL != "https://b.example/eth" || got[1].URL != "https://a.example" {
		t.Fatalf("filtered = %#v", got)
	}
	height, err := MinHeight([]EndpointHeight{{Height: 5}, {Height: 3}, {Height: 9}})
	if err != nil || height != 3 {
		t.Fatalf("min = %d %v", height, err)
	}
	parsed, err := parseHexQuantity("0x10")
	if err != nil || parsed != 16 {
		t.Fatalf("parse = %d %v", parsed, err)
	}
}

func TestSelectByBlockNumberFansOutBatchConcurrentlyAndKeepsRegistryOrder(t *testing.T) {
	const endpointCount = 3
	started := make(chan int, endpointCount)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	servers := make([]*httptest.Server, endpointCount)
	endpoints := make([]chainlist.Endpoint, endpointCount)
	for i := range servers {
		index := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			started <- index
			<-release
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"0x%x"}`, index+1)
		}))
		endpoints[i] = chainlist.Endpoint{URL: servers[i].URL}
		defer servers[i].Close()
	}

	resultCh := make(chan []EndpointHeight, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := SelectByBlockNumber(context.Background(), endpoints, endpointCount, time.Second)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- got
	}()

	seen := make(map[int]bool, endpointCount)
	for len(seen) < endpointCount {
		select {
		case index := <-started:
			seen[index] = true
		case <-time.After(time.Second):
			t.Fatalf("only %d of %d handlers started before release; fan-out was not concurrent", len(seen), endpointCount)
		}
	}
	releaseOnce.Do(func() { close(release) })

	select {
	case err := <-errCh:
		t.Fatal(err)
	case got := <-resultCh:
		if len(got) != endpointCount {
			t.Fatalf("selected %d endpoints, want %d", len(got), endpointCount)
		}
		for i, item := range got {
			if item.URL != endpoints[i].URL || item.Height != uint64(i+1) {
				t.Fatalf("selected[%d] = %#v, want URL %q height %d", i, item, endpoints[i].URL, i+1)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SelectByBlockNumber did not finish after releasing handlers")
	}
}
