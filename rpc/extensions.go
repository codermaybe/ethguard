package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ExtensionKind describes the evidence level of a field outside a method's
// compile-time standard projection.
type ExtensionKind string

const (
	CorroboratedExtension ExtensionKind = "corroborated_extension"
	ProviderResidual      ExtensionKind = "provider_residual"
	ConflictExtension     ExtensionKind = "conflict_field"
)

// ExtensionField is source-backed data attached to a selected core object.
// It carries no protocol semantic claim. CoreObjectFingerprint prevents an
// extension from being detached from the object under which it was observed.
type ExtensionField struct {
	Kind                  ExtensionKind
	Path                  string
	Value                 json.RawMessage
	Sources               []SourceRef
	CoreObjectFingerprint string
}

// ExtensionSet is a read-only-by-convention collection of non-core fields.
// Values and source slices are copied when built by NewExtensionSet.
type ExtensionSet struct {
	Fields []ExtensionField
}

// NewExtensionSet binds fields to the canonical fingerprint of coreValue.
func NewExtensionSet(coreValue json.RawMessage, common []FieldObservation, residuals []FieldObservation, conflicts []ConflictField) (ExtensionSet, error) {
	fingerprint, err := CoreObjectFingerprint(coreValue)
	if err != nil {
		return ExtensionSet{}, err
	}
	set := ExtensionSet{}
	for _, field := range common {
		set.Fields = append(set.Fields, cloneExtension(CorroboratedExtension, field, fingerprint))
	}
	for _, field := range residuals {
		set.Fields = append(set.Fields, cloneExtension(ProviderResidual, field, fingerprint))
	}
	for _, conflict := range conflicts {
		for _, field := range conflict.Values {
			set.Fields = append(set.Fields, cloneExtension(ConflictExtension, field, fingerprint))
		}
	}
	return set, nil
}

// CoreObjectFingerprint returns a stable SHA-256 fingerprint of a JSON core
// object. CanonicalJSON removes object key ordering and formatting differences.
func CoreObjectFingerprint(value json.RawMessage) (string, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

func cloneExtension(kind ExtensionKind, field FieldObservation, fingerprint string) ExtensionField {
	return ExtensionField{
		Kind:                  kind,
		Path:                  field.Path,
		Value:                 append(json.RawMessage(nil), field.Value...),
		Sources:               append([]SourceRef(nil), field.Sources...),
		CoreObjectFingerprint: fingerprint,
	}
}
