package rpc

import "encoding/json"

// The explicit clone family for the known decision graph.
//
// Published results, capture-internal snapshots, and every Result copy must
// own their mutable material: nested values, source lists, conflict arrays,
// extension payloads, and write rejections never alias between two holders.
// These methods enumerate the known types by hand — no reflect deep-copy and
// no top-level slice copy masquerading as isolation. Errors are deliberately
// not cloned: the error interface keeps errors.Is/As, and the immutability
// contract lives on the producer (see the error rules in decision 0066).
// json.RawMessage bytes are mutable slices, so every owned payload copies
// its bytes.

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneSourceRefs(refs []SourceRef) []SourceRef {
	if refs == nil {
		return nil
	}
	out := make([]SourceRef, len(refs))
	copy(out, refs)
	return out
}

func cloneErrors(errs []error) []error {
	if errs == nil {
		return nil
	}
	out := make([]error, len(errs))
	copy(out, errs)
	return out
}

func cloneSourceFaults(faults []SourceFault) []SourceFault {
	if faults == nil {
		return nil
	}
	out := make([]SourceFault, len(faults))
	copy(out, faults)
	return out
}

// Clone returns an independent copy of the evidence. Owned errors stay
// shared by the error-immutability contract; every slice and payload is
// copied.
func (r ReferenceEvidence) Clone() ReferenceEvidence {
	r.Participants = cloneSourceRefs(r.Participants)
	r.Support = cloneSourceRefs(r.Support)
	r.Reject = cloneSourceRefs(r.Reject)
	r.Faults = cloneErrors(r.Faults)
	r.SourceFaults = cloneSourceFaults(r.SourceFaults)
	r.CollectionFaults = cloneErrors(r.CollectionFaults)
	return r
}

// Clone isolates one field observation's value bytes and source list.
func (f FieldObservation) Clone() FieldObservation {
	f.Value = cloneRaw(f.Value)
	f.Sources = cloneSourceRefs(f.Sources)
	return f
}

func cloneFieldObservations(fields []FieldObservation) []FieldObservation {
	if fields == nil {
		return nil
	}
	out := make([]FieldObservation, len(fields))
	for i, field := range fields {
		out[i] = field.Clone()
	}
	return out
}

// Clone isolates one conflict entry and every value it retains.
func (c ConflictField) Clone() ConflictField {
	c.Values = cloneFieldObservations(c.Values)
	return c
}

// Clone isolates the diagnostics arrays: residuals and every conflict's
// values are independent after the call.
func (d FieldDiagnostics) Clone() FieldDiagnostics {
	d.Residuals = cloneFieldObservations(d.Residuals)
	if d.Conflicts == nil {
		return d
	}
	conflicts := make([]ConflictField, len(d.Conflicts))
	for i, conflict := range d.Conflicts {
		conflicts[i] = conflict.Clone()
	}
	d.Conflicts = conflicts
	return d
}

// Clone isolates one extension field's value bytes and source list.
func (e ExtensionField) Clone() ExtensionField {
	e.Value = cloneRaw(e.Value)
	e.Sources = cloneSourceRefs(e.Sources)
	return e
}

// Clone isolates the extension set's fields.
func (s ExtensionSet) Clone() ExtensionSet {
	if s.Fields == nil {
		return s
	}
	fields := make([]ExtensionField, len(s.Fields))
	for i, field := range s.Fields {
		fields[i] = field.Clone()
	}
	s.Fields = fields
	return s
}

// Clone isolates one dimension's supporter lists.
func (d DimensionEvidence) Clone() DimensionEvidence {
	d.Support = cloneSourceRefs(d.Support)
	d.Reject = cloneSourceRefs(d.Reject)
	d.Abstain = cloneSourceRefs(d.Abstain)
	return d
}

func cloneDimensions(dims []DimensionEvidence) []DimensionEvidence {
	if dims == nil {
		return nil
	}
	out := make([]DimensionEvidence, len(dims))
	for i, dim := range dims {
		out[i] = dim.Clone()
	}
	return out
}

// Clone isolates the revert context. It carries only scalars today; the
// method exists so producers and consumers own one isolation seam.
func (r *RevertContext) Clone() *RevertContext {
	if r == nil {
		return nil
	}
	out := *r
	return &out
}

// Clone returns an independent copy of the decision: reference evidence,
// value and provenance bytes, extensions, diagnostics, dimensions, the
// prepared-request snapshot, and every source list. Faults keep their error
// values (errors are immutable once published) but get their own slices.
func (d Decision) Clone() Decision {
	if d.Reference != nil {
		ref := d.Reference.Clone()
		d.Reference = &ref
	}
	d.Participants = cloneSourceRefs(d.Participants)
	d.Value = cloneRaw(d.Value)
	d.Revert = d.Revert.Clone()
	if d.Extensions != nil {
		ext := d.Extensions.Clone()
		d.Extensions = &ext
	}
	if d.Diagnostics != nil {
		diag := d.Diagnostics.Clone()
		d.Diagnostics = &diag
	}
	d.Support = cloneSourceRefs(d.Support)
	d.Reject = cloneSourceRefs(d.Reject)
	d.Faults = cloneErrors(d.Faults)
	d.Prepared = d.Prepared.Clone()
	d.SourceFaults = cloneSourceFaults(d.SourceFaults)
	d.CollectionFaults = cloneErrors(d.CollectionFaults)
	d.Provenance = cloneRaw(d.Provenance)
	d.Dimensions = cloneDimensions(d.Dimensions)
	if d.DeletedFields != nil {
		d.DeletedFields = append([]string(nil), d.DeletedFields...)
	}
	return d
}

// Clone isolates one structured protocol error's data bytes.
func (e *RPCError) Clone() *RPCError {
	if e == nil {
		return nil
	}
	out := *e
	out.Data = cloneRaw(out.Data)
	return &out
}

// Clone isolates one source answer, including its rejection body.
func (a SourceAnswer) Clone() SourceAnswer {
	a.Rejection = a.Rejection.Clone()
	return a
}

func cloneSourceAnswers(answers []SourceAnswer) []SourceAnswer {
	if answers == nil {
		return nil
	}
	out := make([]SourceAnswer, len(answers))
	for i, answer := range answers {
		out[i] = answer.Clone()
	}
	return out
}

// Clone returns an independent copy of the write decision: every per-source
// answer, the silent list, and the fault slice. Errors stay shared by the
// error-immutability contract.
func (w WriteDecision) Clone() WriteDecision {
	w.Accepted = cloneSourceAnswers(w.Accepted)
	w.Dissent = cloneSourceAnswers(w.Dissent)
	w.Rejected = cloneSourceAnswers(w.Rejected)
	w.Silent = cloneSourceRefs(w.Silent)
	w.Faults = cloneErrors(w.Faults)
	return w
}
