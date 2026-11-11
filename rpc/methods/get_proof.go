package methods

// methodGetProof is the JSON-RPC method spelling this file owns. The method
// stays explicitly unsupported: no account or storage proof admission
// contract exists yet, so it must never fall through to a transparent
// forwarding that could be mistaken for a corroboration guarantee. The
// constant is deliberately unexported: an exported name would advertise a
// public surface the catalog explicitly does not provide.
const methodGetProof = "eth_getProof"

func getProofDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:     methodGetProof,
		Kind:     UnsupportedMethod,
		Family:   FamilyVerification,
		Coverage: CoverageUnsupported,
		Notes:    "Not registered; no account/storage proof admission contract yet.",
	}
}
