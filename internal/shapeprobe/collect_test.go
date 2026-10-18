package shapeprobe

import (
	"testing"

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
