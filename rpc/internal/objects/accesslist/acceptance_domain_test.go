package accesslist

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The protected value of eth_createAccessList must not move when a member the
// comparison mask drops changes. encoding/json matches struct members
// case-insensitively with the last occurrence winning, so the object rules
// inherit exactly that matching: a case-variant alias is the same member, and
// a vendor member — however spelled, whatever its shape — never rewrites the
// resolved value or the error the encode stage reports.

// mustEncode renders one accepted result's canonical bytes.
func mustEncode(t *testing.T, raw string) string {
	t.Helper()
	out, err := Encode(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Encode(%s) = %v", raw, err)
	}
	return string(out)
}

func TestAccessListVendorMembersNeverRewriteTheValue(t *testing.T) {
	duplicate := `{"accessList":[],"error":"a","Error":"b"}`
	want := mustEncode(t, duplicate)
	if !strings.Contains(want, `"error":"b"`) {
		t.Fatalf("duplicate case-variant members must resolve last-wins, got %s", want)
	}
	for _, vendor := range []string{`null`, `"x"`, `{"v":1}`, `[1,2]`} {
		withVendor := `{"accessList":[],"error":"a","Error":"b","vendor":` + vendor + `}`
		if got := mustEncode(t, withVendor); got != want {
			t.Fatalf("vendor member %s changed the value:\n without %s\n with    %s", vendor, want, got)
		}
	}
}

func TestAccessListAcceptsCaseAliasesOfEveryMember(t *testing.T) {
	if got := mustEncode(t, `{"ACCESSLIST":[]}`); got != `{"accessList":[]}` {
		t.Fatalf("upper-case alias rejected: %s", got)
	}
	canonical := mustEncode(t, `{"accessList":[{"address":"`+"0x"+strings.Repeat("a", 40)+`","storageKeys":[]}],"gasUsed":"0x0","error":"e"}`)
	aliases := mustEncode(t, `{"GASUSED":"0x0","accessList":[{"ADDRESS":"`+"0x"+strings.Repeat("A", 40)+`","STORAGEKEYS":[]}],"ERROR":"e"}`)
	if canonical != aliases {
		t.Fatalf("case aliases changed the canonical value:\n canonical %s\n aliases   %s", canonical, aliases)
	}
}

func TestAccessListFieldOrderDoesNotChangeTheValue(t *testing.T) {
	first := mustEncode(t, `{"accessList":[],"gasUsed":"0x1","error":"e"}`)
	second := mustEncode(t, `{"error":"e","accessList":[],"gasUsed":"0x1"}`)
	if first != second {
		t.Fatalf("field order changed the value: %s vs %s", first, second)
	}
}

func TestAccessListItemPresencePrecedesValueValidation(t *testing.T) {
	_, err := Encode(json.RawMessage(`{"accessList":[{"address":"bad"}]}`))
	if err == nil || !strings.Contains(err.Error(), "missing storageKeys") {
		t.Fatalf("item with an invalid address and no storageKeys must report the missing member first, got %v", err)
	}
	_, err = Encode(json.RawMessage(`{"accessList":[{"storageKeys":[]}]}`))
	if err == nil || !strings.Contains(err.Error(), "missing address") {
		t.Fatalf("item without an address must report the missing member, got %v", err)
	}
	_, err = Encode(json.RawMessage(`{"accessList":[5]}`))
	if err == nil || !strings.Contains(err.Error(), "expected an object") {
		t.Fatalf("non-object item must report the shape, got %v", err)
	}
}

func TestAccessListOmittedAndNullMembersStayOmitted(t *testing.T) {
	if got := mustEncode(t, `{"accessList":[]}`); got != `{"accessList":[]}` {
		t.Fatalf("absent error and gasUsed must stay omitted, got %s", got)
	}
	if got := mustEncode(t, `{"accessList":[],"error":null,"gasUsed":null}`); got != `{"accessList":[]}` {
		t.Fatalf("null error and gasUsed must fold to omission, got %s", got)
	}
	if got := mustEncode(t, `{"accessList":[],"error":""}`); got != `{"accessList":[],"error":""}` {
		t.Fatalf("an empty error string is present, got %s", got)
	}
}

func TestAccessListEmptyArraysStayArrays(t *testing.T) {
	got := mustEncode(t, `{"accessList":[{"address":"`+"0x"+strings.Repeat("a", 40)+`","storageKeys":[]}],"gasUsed":"0x0"}`)
	if !bytes.Contains([]byte(got), []byte(`"storageKeys":[]`)) || !strings.Contains(got, `"gasUsed":"0x0"`) {
		t.Fatalf("empty storageKeys must stay [], got %s", got)
	}
	if got := mustEncode(t, `{"accessList":[]}`); got != `{"accessList":[]}` {
		t.Fatalf("empty accessList must stay [], got %s", got)
	}
}

func TestAccessListDuplicateAliasesFollowDocumentOrder(t *testing.T) {
	// The last occurrence wins, exactly as a struct decode of the same
	// document resolves it — independent of alphabetical re-rendering.
	if got := mustEncode(t, `{"accessList":[],"error":"first","error":"second"}`); !strings.Contains(got, `"error":"second"`) {
		t.Fatalf("duplicate member must keep the last occurrence, got %s", got)
	}
}

// Item members decode with the item struct's own types: storageKeys is an
// array member, so a value of any other JSON type is a decode error the
// encode stage has always reported as a malformed item shape — regardless of
// a later alias carrying a legal array, of which alias spelling comes first,
// or of which other errors the same item also has.

func TestAccessListItemTypeErrorsRejectLikeTheItemStruct(t *testing.T) {
	rejects := []string{
		`{"accessList":[{"address":"0x` + strings.Repeat("11", 20) + `","StorageKeys":123,"storageKeys":[]}]}`,
		`{"accessList":[{"address":"0x` + strings.Repeat("11", 20) + `","storageKeys":123,"StorageKeys":[]}]}`,
		`{"accessList":[{"storageKeys":123}]}`,
		`{"accessList":[{"address":"bad","storageKeys":123}]}`,
		`{"accessList":[{"address":"0x` + strings.Repeat("11", 20) + `","storageKeys":{}}]}`,
		`{"accessList":[{"address":"0x` + strings.Repeat("11", 20) + `","storageKeys":"0x` + strings.Repeat("b", 64) + `"}]}`,
	}
	for _, raw := range rejects {
		if _, err := Encode(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "expected an object") {
			t.Fatalf("storageKeys type error must reject the item shape, got %v (input %s)", err, raw)
		}
	}
}

func TestAccessListItemErrorsFollowDocumentOrder(t *testing.T) {
	// Items validate one at a time: an earlier item's value error surfaces
	// before a later item's missing member, exactly as the per-item pass has
	// always reported.
	first, err := Encode(json.RawMessage(`{"accessList":[{"address":"bad","storageKeys":[]},{}]}`))
	if err == nil || !strings.Contains(err.Error(), "accessList[0]") || !strings.Contains(err.Error(), "address") {
		t.Fatalf("item 0's value error must come first, got out=%s err=%v", first, err)
	}
	second, err := Encode(json.RawMessage(`{"accessList":[{"address":"bad","storageKeys":[]},{"address":"0x` + strings.Repeat("11", 20) + `"}]}`))
	if err == nil || !strings.Contains(err.Error(), "accessList[0]") || !strings.Contains(err.Error(), "address") {
		t.Fatalf("item 0's value error must precede item 1's missing member, got out=%s err=%v", second, err)
	}
}

func TestAccessListTopLevelScalarAliasesKeepStructSemantics(t *testing.T) {
	// Top-level members decode into raw holders: a scalar type error never
	// rejects the document, the last occurrence of a member wins, and null
	// folds to absence — in both alias orders.
	if got := mustEncode(t, `{"accessList":[],"error":null,"Error":"b"}`); !strings.Contains(got, `"error":"b"`) {
		t.Fatalf("null then legal alias must resolve to the legal value, got %s", got)
	}
	if got := mustEncode(t, `{"accessList":[],"error":"a","Error":null}`); got != `{"accessList":[]}` {
		t.Fatalf("legal then null alias must fold to omission, got %s", got)
	}
	if _, err := Encode(json.RawMessage(`{"accessList":[],"ACCESSLIST":null}`)); err == nil || !strings.Contains(err.Error(), "missing accessList") {
		t.Fatalf("a null alias overwrites the member: the required accessList goes missing, got %v", err)
	}
	if got := mustEncode(t, `{"accessList":null,"ACCESSLIST":[]}`); got != `{"accessList":[]}` {
		t.Fatalf("null canonical then legal alias must resolve to the alias, got %s", got)
	}
	if _, err := Encode(json.RawMessage(`{"accessList":[],"error":5,"error":"x"}`)); err != nil {
		t.Fatalf("a raw-typed member accepts any JSON shape with last-wins, got %v", err)
	}
}
