package pool

import (
	"encoding/json"
	"testing"
)

type rawBodyTestRPCError struct{ code int }

func (e rawBodyTestRPCError) Error() string { return "execution reverted" }

func (e rawBodyTestRPCError) ErrorCode() int { return e.code }

func TestPreserveErrorDataDistinguishesMissingAndNull(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		present bool
		data    string
	}{
		{name: "missing", body: `{"error":{"code":3,"message":"execution reverted"}}`},
		{name: "null", body: `{"error":{"code":3,"message":"execution reverted","data":null}}`, present: true, data: "null"},
		{name: "hex", body: `{"error":{"code":3,"message":"execution reverted","data":"0xdead"}}`, present: true, data: `"0xdead"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := preserveErrorData(rawBodyTestRPCError{code: 3}, []byte(tc.body))
			preserved, ok := err.(*preservedDataError)
			if !ok {
				t.Fatalf("error type = %T, want preservedDataError", err)
			}
			got, gotPresent := preserved.ErrorDataJSON()
			if gotPresent != tc.present || string(got) != tc.data {
				t.Fatalf("data = %s/%v, want %s/%v", got, gotPresent, tc.data, tc.present)
			}
			if tc.present && !json.Valid(got) {
				t.Fatalf("preserved data is not JSON: %s", got)
			}
		})
	}
}
