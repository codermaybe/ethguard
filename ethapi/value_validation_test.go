package ethapi_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func valuePointer[T any](v T) *T { return &v }

func accessListValue() ethapi.AccessListResult {
	return ethapi.AccessListResult{
		AccessList: valuePointer([]ethapi.AccessListItem{{
			Address:     ethapi.Address("0x" + strings.Repeat("Ab", 20)),
			StorageKeys: []ethapi.Hash{ethapi.Hash("0x" + strings.Repeat("ab", 32))},
		}}),
		GasUsed: valuePointer(ethapi.Quantity("0x5208")),
		Error:   valuePointer(ethapi.Text("<&>\x00")),
	}
}

func cloneAccessListValue(v ethapi.AccessListResult) ethapi.AccessListResult {
	out := v
	if v.AccessList != nil {
		var items []ethapi.AccessListItem
		if *v.AccessList != nil {
			items = append(make([]ethapi.AccessListItem, 0, len(*v.AccessList)), (*v.AccessList)...)
			for i := range items {
				if items[i].StorageKeys != nil {
					items[i].StorageKeys = append(make([]ethapi.Hash, 0, len(items[i].StorageKeys)), items[i].StorageKeys...)
				}
			}
		}
		out.AccessList = &items
	}
	if v.Error != nil {
		out.Error = valuePointer(*v.Error)
	}
	if v.GasUsed != nil {
		out.GasUsed = valuePointer(*v.GasUsed)
	}
	return out
}

func checkAccessListValueContract(t *testing.T, v ethapi.AccessListResult) bool {
	t.Helper()
	before := cloneAccessListValue(v)
	raw, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	_, parseErr := ethapi.NewCreateAccessListResult(raw)
	valueErr := ethapi.ValidateCreateAccessListResult(v)
	if (valueErr == nil) != (parseErr == nil) {
		t.Fatalf("acceptance differs for %s: value=%v constructor=%v", raw, valueErr, parseErr)
	}
	if !reflect.DeepEqual(v, before) {
		t.Fatalf("validation changed value: before=%+v after=%+v", before, v)
	}
	return valueErr == nil
}

func TestValidateCreateAccessListResultContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*ethapi.AccessListResult)
		valid bool
	}{
		{"mixed-case-address", func(*ethapi.AccessListResult) {}, true},
		{"omitted", func(v *ethapi.AccessListResult) { *v = ethapi.AccessListResult{} }, true},
		{"null-array", func(v *ethapi.AccessListResult) { v.AccessList = valuePointer([]ethapi.AccessListItem(nil)) }, false},
		{"empty-array", func(v *ethapi.AccessListResult) { v.AccessList = valuePointer([]ethapi.AccessListItem{}) }, true},
		{"null-storage", func(v *ethapi.AccessListResult) { (*v.AccessList)[0].StorageKeys = nil }, false},
		{"empty-storage", func(v *ethapi.AccessListResult) { (*v.AccessList)[0].StorageKeys = []ethapi.Hash{} }, true},
		{"zero-item", func(v *ethapi.AccessListResult) { (*v.AccessList)[0] = ethapi.AccessListItem{} }, false},
		{"forged-address", func(v *ethapi.AccessListResult) {
			(*v.AccessList)[0].Address = "0X" + ethapi.Address(strings.Repeat("ab", 20))
		}, false},
		{"forged-hash", func(v *ethapi.AccessListResult) {
			(*v.AccessList)[0].StorageKeys[0] = "0x" + ethapi.Hash(strings.Repeat("AB", 32))
		}, false},
		{"forged-quantity", func(v *ethapi.AccessListResult) { v.GasUsed = valuePointer(ethapi.Quantity("0x01")) }, false},
		{"empty-quantity", func(v *ethapi.AccessListResult) { v.GasUsed = valuePointer(ethapi.Quantity("")) }, false},
		{"invalid-utf8-text", func(v *ethapi.AccessListResult) { v.Error = valuePointer(ethapi.Text("\xff\xfe\xc0\xaf")) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := accessListValue()
			tc.edit(&v)
			if valid := checkAccessListValueContract(t, v); valid != tc.valid {
				t.Fatalf("valid=%v want %v", valid, tc.valid)
			}
		})
	}
	v := accessListValue()
	if allocations := testing.AllocsPerRun(100, func() {
		if err := ethapi.ValidateCreateAccessListResult(v); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("valid value allocated %v times", allocations)
	}
}

func FuzzValidateCreateAccessListResult(f *testing.F) {
	f.Add("0x"+strings.Repeat("Ab", 20), "0x"+strings.Repeat("ab", 32), "0x0", "\xff\xfe", byte(0))
	f.Add("bad", "bad", "0x01", "error", byte(31))
	f.Fuzz(func(t *testing.T, address, hash, quantity, text string, shape byte) {
		v := ethapi.AccessListResult{
			AccessList: valuePointer([]ethapi.AccessListItem{{Address: ethapi.Address(address), StorageKeys: []ethapi.Hash{ethapi.Hash(hash)}}}),
			GasUsed:    valuePointer(ethapi.Quantity(quantity)), Error: valuePointer(ethapi.Text(text)),
		}
		switch shape & 3 {
		case 1:
			v.AccessList = nil
		case 2:
			*v.AccessList = nil
		case 3:
			*v.AccessList = []ethapi.AccessListItem{}
		}
		if shape&4 != 0 {
			v.GasUsed = nil
		}
		if shape&8 != 0 {
			v.Error = nil
		}
		if shape&16 != 0 && v.AccessList != nil && len(*v.AccessList) > 0 {
			(*v.AccessList)[0].StorageKeys = nil
		}
		checkAccessListValueContract(t, v)
	})
}

func BenchmarkAccessListValueValidation(b *testing.B) {
	for _, size := range []int{0, 1, 32, 256} {
		v := accessListValue()
		items := make([]ethapi.AccessListItem, size)
		for i := range items {
			items[i] = (*v.AccessList)[0]
		}
		v.AccessList = &items
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.Run("Value", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := ethapi.ValidateCreateAccessListResult(v); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("MarshalAndParse", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					raw, err := v.MarshalJSON()
					if err != nil {
						b.Fatal(err)
					}
					if _, err := ethapi.NewCreateAccessListResult(raw); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
