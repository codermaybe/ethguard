// Package limits holds the cross-package response-size bound. Three layers
// enforce the same number on purpose — jsonsafe validates a payload already
// in memory, the capture transport bounds how much of the wire it records,
// and the native adapter bounds one response body read — so a body that one
// layer admits cannot be rejected by another purely on size. The constant
// lives here so the three spellings cannot drift.
package limits

// MaxBodyBytes bounds one response body. It matches the audited default.
const MaxBodyBytes int64 = 32 << 20
