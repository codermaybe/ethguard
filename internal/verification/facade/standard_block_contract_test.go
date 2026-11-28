package ethguard_test

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
)

// A generic false/fullTx read may deliberately return only an identity or a
// header. Its corroboration is not a claim of complete official Block shape.
// Exercise all three branches through the public facade so consumer evidence
// cannot accidentally treat the complete-object fixture as universal coverage.
func TestStandardBlockConsumptionBranches(t *testing.T) {
	full := fixtureBlock(t, 16)
	hash := ethapi.Hash(blockHashOf(t, full))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(full, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"transactions", "uncles", "size"} {
		delete(fields, field)
	}
	header, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := json.Marshal(map[string]any{"number": "0x10", "hash": hash})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		raw           json.RawMessage
		block, header bool
	}{
		{"full", full, true, true},
		{"header", header, false, true},
		{"reference", reference, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, nodes := fleet(t, 3)
			for _, node := range nodes {
				node.Return(methods.MethodGetBlockByHash, tc.raw)
				node.Return(methods.MethodGetBlockByNumber, tc.raw)
			}
			ctx := context.Background()
			for _, request := range []struct {
				method string
				ref    any
			}{
				{methods.MethodGetBlockByHash, hash},
				{methods.MethodGetBlockByNumber, "0x10"},
			} {
				result, err := client.Call(ctx, request.method, request.ref, false)
				if err != nil || !result.IsCorroborated() || result.Decision == nil {
					t.Fatalf("generic %s did not retain %s contract: %v %+v", request.method, tc.name, err, result)
				}
				_, blockErr := ethapi.NewBlock(result.Decision.Value)
				_, headerErr := ethapi.NewHeader(result.Decision.Value)
				// A complete Block carries body members forbidden by Header's
				// exact schema; typed Header performs its explicit projection.
				if (blockErr == nil) != tc.block || (headerErr == nil) != (tc.name == "header") {
					t.Fatalf("%s branch evidence overstated schema guarantee: block=%v header=%v", tc.name, blockErr, headerErr)
				}
			}
			blockByHash, _, blockHashErr := client.BlockByHash(ctx, hash)
			blockByNumber, _, blockNumberErr := client.BlockByNumber(ctx, big.NewInt(16))
			if (blockHashErr == nil) != tc.block || (blockByHash != nil) != tc.block || (blockNumberErr == nil) != tc.block || (blockByNumber != nil) != tc.block {
				t.Fatalf("typed Block branch=%s hash=%v number=%v", tc.name, blockHashErr, blockNumberErr)
			}
			headerByHash, _, headerHashErr := client.HeaderByHash(ctx, hash)
			headerByNumber, _, headerNumberErr := client.HeaderByNumber(ctx, big.NewInt(16))
			if (headerHashErr == nil) != tc.header || (headerByHash != nil) != tc.header || (headerNumberErr == nil) != tc.header || (headerByNumber != nil) != tc.header {
				t.Fatalf("typed Header branch=%s hash=%v number=%v", tc.name, headerHashErr, headerNumberErr)
			}
		})
	}
}
