// Package wire owns shared JSON wire normalization, schema-driven pruning,
// and result types generated from the pinned execution-apis bundle.
//
// Generated New* constructors check supported official result shapes,
// required fields, value constraints and nullability. Shared normalizers
// define canonical bytes; the mask compiler builds comparison whitelists.
// These responsibilities do not replace object and method admission:
// request binding, content commitments, protected-field projection and
// provider agreement remain outside this package.
//
// Official numeric bounds come from the schema and use exact decimal
// comparison. The protected encoder's CanonicalRatio policy is separate
// from the generated Number type and must not narrow the official model.
package wire
