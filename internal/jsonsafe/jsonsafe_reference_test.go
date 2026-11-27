package jsonsafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Frozen from c8c7f82, including its effective resource limits. This oracle must
// not call the production Validate or any production traversal helper.
const (
	referenceMaxBodyBytes    = 32 << 20
	referenceMaxNestingDepth = 32
	referenceMaxObjectKeys   = 4096
)

func validateReference(raw []byte) error {
	if int64(len(raw)) > referenceMaxBodyBytes {
		return ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkReference(decoder, 0); err != nil {
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

// decodeReference validates raw and then decodes it into dst. The second decode is
// intentional: the standard library's type handling remains authoritative,
// while this package owns only input limits and duplicate-key detection.
func decodeReference(raw []byte, dst any) error {
	if err := validateReference(raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func walkReference(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			if depth >= referenceMaxNestingDepth {
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
				if len(seen) > referenceMaxObjectKeys {
					return ErrTooManyKeys
				}
				if err := walkReference(decoder, depth+1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			if depth >= referenceMaxNestingDepth {
				return ErrTooDeep
			}
			for decoder.More() {
				if err := walkReference(decoder, depth+1); err != nil {
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
