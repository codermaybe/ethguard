package rpc

import "encoding/json"

// representativeCandidate is one admitted supporter of a winning cluster, in
// the order the caller captured the voting members. Strict, CallRevert, and
// Median all hand their winning cluster to firstRepresentative, so the three
// resolvers pick the same person.
type representativeCandidate struct {
	source    SourceRef
	value     json.RawMessage
	evidence  json.RawMessage
	canonical json.RawMessage
}

// firstRepresentative returns the first supporter in captured order. Its
// value is that source's admitted payload and its evidence is that source's
// own admission evidence, copied so the caller cannot mutate it later. When
// the first supporter has no evidence the evidence stays empty: a later
// supporter's evidence is never substituted. The caller must pass a non-empty
// cluster.
func firstRepresentative(cluster []representativeCandidate) representativeCandidate {
	first := cluster[0]
	first.value = append(json.RawMessage(nil), first.value...)
	first.evidence = append(json.RawMessage(nil), first.evidence...)
	return first
}
