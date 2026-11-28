package subscription

import (
	"reflect"
	"unsafe"
)

// transferValueBytes walks only the closed, acyclic subscription DTO shapes.
// It counts complete slice capacity and pointed-to storage before ownership
// leaves the source. Shared pointers are deliberately charged for each use.
// The factor two covers allocation rounding; metadata and release closures have
// a separate fixed allowance. Unsupported shapes fail closed by saturating.
func transferValueBytes(value any) uint64 {
	n := ownedDTOBytes(reflect.ValueOf(value))
	if n > (^uint64(0)-4096)/2 {
		return ^uint64(0)
	}
	return 2*n + 4096
}

var auditRangeType = reflect.TypeFor[AuditRange]()

func ownedDTOBytes(v reflect.Value) uint64 {
	if !v.IsValid() {
		return 0
	}
	n := uint64(v.Type().Size())
	if v.Type() == auditRangeType {
		a := v.Interface().(AuditRange)
		for record := range a.All() {
			n = saturatingAdd(n, uint64(unsafe.Sizeof(auditEntry{}))+256+uint64(len(record.Session.SourceID)))
		}
		return n
	}
	switch v.Kind() {
	case reflect.String:
		return saturatingAdd(n, uint64(v.Len()))
	case reflect.Pointer:
		if !v.IsNil() {
			n = saturatingAdd(n, ownedDTOBytes(v.Elem()))
		}
	case reflect.Slice:
		if v.Cap() != 0 {
			size := uint64(v.Type().Elem().Size())
			if size != 0 && uint64(v.Cap()) > ^uint64(0)/size {
				return ^uint64(0)
			}
			n = saturatingAdd(n, uint64(v.Cap())*size)
			for i := 0; i < v.Len(); i++ {
				child := ownedDTOBytes(v.Index(i))
				if child >= size {
					n = saturatingAdd(n, child-size)
				}
			}
		}
	case reflect.Array:
		size := uint64(v.Type().Elem().Size())
		for i := 0; i < v.Len(); i++ {
			child := ownedDTOBytes(v.Index(i))
			if child >= size {
				n = saturatingAdd(n, child-size)
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			child := ownedDTOBytes(v.Field(i))
			size := uint64(v.Field(i).Type().Size())
			if child >= size {
				n = saturatingAdd(n, child-size)
			}
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		return ^uint64(0)
	}
	return n
}
func saturatingAdd(a, b uint64) uint64 {
	if b > ^uint64(0)-a {
		return ^uint64(0)
	}
	return a + b
}
