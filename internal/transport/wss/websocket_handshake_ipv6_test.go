package wss

import (
	"net"
	"net/url"
	"testing"
)

// TestHostPortForSchemeIPV6Literals pins the dial-address grammar for IPv6
// literals: url.Parse keeps the square brackets on u.Host, so the default
// port must be joined onto the bracket-stripped u.Hostname(). Joining u.Host
// would double-bracket the literal into an address net.Dial rejects with
// "missing port in address".
func TestHostPortForSchemeIPV6Literals(t *testing.T) {
	cases := []struct {
		raw    string
		secure bool
		want   string
	}{
		{"ws://[::1]/", false, "[::1]:80"},
		{"wss://[2001:db8::1]/", true, "[2001:db8::1]:443"},
		{"ws://[::1]:8546/", false, "[::1]:8546"},
		{"wss://[2001:db8::1]:8545/", true, "[2001:db8::1]:8545"},
		{"ws://example.com/", false, "example.com:80"},
		{"wss://example.com/", true, "example.com:443"},
		{"wss://EXAMPLE.com:443/", true, "EXAMPLE.com:443"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.raw, err)
		}
		got, err := hostPortForScheme(u, tc.secure)
		if err != nil {
			t.Fatalf("%s: hostPortForScheme: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("%s: dial address %q, want %q", tc.raw, got, tc.want)
		}
		// The produced address must be acceptable to the net package's own
		// parser — the property a double-bracketed literal violates.
		if _, _, err := net.SplitHostPort(got); err != nil {
			t.Fatalf("%s: dial address %q is not a valid host:port: %v", tc.raw, got, err)
		}
	}
}
