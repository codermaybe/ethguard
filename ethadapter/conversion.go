package ethadapter

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum/common"
)

// ConversionError identifies a value the target cannot faithfully represent.
type ConversionError struct {
	Type, Field, Reason string
	Err                 error
}

func (e *ConversionError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("ethadapter: %s.%s: %s: %v", e.Type, e.Field, e.Reason, e.Err)
	}
	return fmt.Sprintf("ethadapter: %s.%s: %s", e.Type, e.Field, e.Reason)
}
func (e *ConversionError) Unwrap() error { return e.Err }
func conversionError(kind, field, reason string, err error) error {
	return &ConversionError{Type: kind, Field: field, Reason: reason, Err: err}
}

// ConversionOmission records a structural field or presence distinction that
// the destination does not carry. It never excuses malformed data.
type ConversionOmission struct{ Field, Reason string }

// ConversionReport is returned by pure conversions and delivered to observers.
// Without an observer, Client methods intentionally do not deliver this report.
type ConversionReport struct{ Omissions []ConversionOmission }

func (r *ConversionReport) add(field, reason string) {
	for _, existing := range r.Omissions {
		if existing.Field == field && existing.Reason == reason {
			return
		}
	}
	r.Omissions = append(r.Omissions, ConversionOmission{Field: field, Reason: reason})
}
func (r *ConversionReport) merge(other ConversionReport) {
	for _, omission := range other.Omissions {
		r.add(omission.Field, omission.Reason)
	}
}

type converter struct {
	kind   string
	err    error
	report ConversionReport
}

func (c *converter) fail(field, reason string, err error) {
	if c.err == nil {
		c.err = conversionError(c.kind, field, reason, err)
	}
}
func (c *converter) big(field, value string) *big.Int {
	q, err := ethapi.NewQuantity(json.RawMessage(strconv.Quote(value)))
	if err != nil {
		c.fail(field, "invalid unsigned quantity", err)
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(string(q)[2:], 16)
	if !ok {
		c.fail(field, "invalid unsigned quantity", nil)
		return new(big.Int)
	}
	return n
}
func (c *converter) u64(field, value string) uint64 {
	n := c.big(field, value)
	if !n.IsUint64() {
		c.fail(field, "overflows uint64", nil)
		return 0
	}
	return n.Uint64()
}
func (c *converter) uint(field, value string) uint {
	n := c.u64(field, value)
	if strconv.IntSize == 32 && n > 1<<32-1 {
		c.fail(field, "overflows uint", nil)
		return 0
	}
	return uint(n)
}
func (c *converter) bytes(field, value string, size int) []byte {
	d, err := ethapi.NewData(json.RawMessage(strconv.Quote(value)))
	if err != nil {
		c.fail(field, "invalid hex data", err)
		return nil
	}
	b, err := hex.DecodeString(string(d)[2:])
	if err != nil || (size >= 0 && len(b) != size) {
		c.fail(field, fmt.Sprintf("expected %d bytes", size), err)
		return nil
	}
	return b
}
func (c *converter) hash(field, value string) common.Hash {
	var h common.Hash
	copy(h[:], c.bytes(field, value, len(h)))
	return h
}
func (c *converter) address(field, value string) common.Address {
	var a common.Address
	copy(a[:], c.bytes(field, value, len(a)))
	return a
}
func (c *converter) optionalBig(field string, value *ethapi.Quantity) *big.Int {
	if value == nil {
		return nil
	}
	return c.big(field, string(*value))
}
func (c *converter) optionalU64(field string, value *ethapi.Quantity) *uint64 {
	if value == nil {
		return nil
	}
	n := c.u64(field, string(*value))
	return &n
}
func (c *converter) optionalHash(field string, value *ethapi.Hash) *common.Hash {
	if value == nil {
		return nil
	}
	h := c.hash(field, string(*value))
	return &h
}

func address(a common.Address) ethapi.Address { return ethapi.Address(a.Hex()) }
func hash(h common.Hash) ethapi.Hash          { return ethapi.Hash(h.Hex()) }
func quantity(n uint64) ethapi.Quantity       { return ethapi.Quantity(fmt.Sprintf("0x%x", n)) }
func data(b []byte) ethapi.Data               { return ethapi.Data("0x" + hex.EncodeToString(b)) }

func (c *converter) hexByte(field, value string) byte {
	q, err := ethapi.NewHexByte(json.RawMessage(strconv.Quote(value)))
	if err != nil {
		c.fail(field, "invalid hex byte", err)
		return 0
	}
	n, err := strconv.ParseUint(string(q)[2:], 16, 8)
	if err != nil {
		c.fail(field, "invalid hex byte", err)
	}
	return byte(n)
}
