// Package syncing owns source progress projection before the official schema.
package syncing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

var fields = func() []string {
	t := reflect.TypeOf(ethapi.SyncProgress{})
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}()

// Decode keeps false or the official progress fields, folding only their
// hexadecimal spelling before the strict generated constructor. Provider
// counters remain source evidence and never enter the official result.
func Decode(raw json.RawMessage) (ethapi.SyncingStatus, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return ethapi.SyncingStatus{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		return ethapi.NewSyncingResult(raw)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil || source == nil {
		return ethapi.SyncingStatus{}, fmt.Errorf("syncing: expected false or an object")
	}
	candidate := make(map[string]json.RawMessage, len(fields))
	for _, name := range fields {
		if value, present := source[name]; present {
			candidate[name] = ethcodec.FoldHexSpelling(value)
		}
	}
	projected, err := ethcodec.MarshalNoEscape(candidate)
	if err != nil {
		return ethapi.SyncingStatus{}, err
	}
	return ethapi.NewSyncingResult(projected)
}
