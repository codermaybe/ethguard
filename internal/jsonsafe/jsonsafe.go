// Package jsonsafe validates JSON inputs before they reach method-specific
// admission or typed projection. It deliberately has no knowledge of RPC
// methods or Ethereum types.
package jsonsafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	MaxBodyBytes    int64 = 32 << 20
	MaxNestingDepth       = 32
	MaxObjectKeys         = 4096
)

var (
	ErrTooLarge     = errors.New("jsonsafe: body too large")
	ErrTooDeep      = errors.New("jsonsafe: nesting depth exceeded")
	ErrDuplicateKey = errors.New("jsonsafe: duplicate object key")
	ErrTooManyKeys  = errors.New("jsonsafe: object key limit exceeded")
	ErrTrailingData = errors.New("jsonsafe: trailing data")
)

// Validate checks one complete JSON value against the resource and syntax
// limits. It does not retain a decoded representation.
func Validate(raw []byte) error {
	if int64(len(raw)) > MaxBodyBytes {
		return ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walk(decoder, 0); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ErrTrailingData
		}
		return fmt.Errorf("%w: %v", ErrTrailingData, err)
	}
	return nil
}

// Decode validates raw and then decodes it into dst. The second decode is
// intentional: the standard library's type handling remains authoritative,
// while this package owns only input limits and duplicate-key detection.
func Decode(raw []byte, dst any) error {
	if err := Validate(raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func walk(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			if depth >= MaxNestingDepth {
				return ErrTooDeep
			}
			seen := make(map[string]struct{})
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return errors.New("jsonsafe: object key is not a string")
				}
				if _, exists := seen[name]; exists {
					return fmt.Errorf("%w: %q", ErrDuplicateKey, name)
				}
				seen[name] = struct{}{}
				if len(seen) > MaxObjectKeys {
					return ErrTooManyKeys
				}
				if err := walk(decoder, depth+1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			if depth >= MaxNestingDepth {
				return ErrTooDeep
			}
			for decoder.More() {
				if err := walk(decoder, depth+1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("jsonsafe: unexpected closing delimiter")
		}
	default:
		return nil
	}
}
