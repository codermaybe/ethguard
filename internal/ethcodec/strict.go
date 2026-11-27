package ethcodec

// These exact schema patterns are shared by generated constructors and
// runtime scalar consumers. Compatibility never runs inside these predicates.
const (
	QuantityPattern      = `^0x(0|[1-9a-f][0-9a-f]*)$`
	Quantity64Pattern    = `^0x(0|[1-9a-f][0-9a-f]{0,15})$`
	Quantity256Pattern   = `^0x(0|[1-9a-f][0-9a-f]{0,63})$`
	DataPattern          = `^0x([0-9a-f]{2})*$`
	AddressPattern       = `^0x[0-9a-fA-F]{40}$`
	HexBytePattern       = `^0x[0-9a-f]{1,2}$`
	StorageKeyPattern    = `^0x[0-9a-f]{0,64}$`
	DecimalStringPattern = `^(0|[1-9][0-9]*)$`
)

func IsCanonicalQuantity(text string) bool {
	if len(text) < 3 {
		return false
	}
	if text[2] == '0' && len(text) != 3 {
		return false
	}
	return IsLowerFixedHex(text, len(text)-2)
}

func IsCanonicalQuantity64(text string) bool {
	return len(text) <= 18 && IsCanonicalQuantity(text)
}

func IsCanonicalQuantity256(text string) bool {
	return len(text) <= 66 && IsCanonicalQuantity(text)
}

func IsWholeByteData(text string) bool {
	return len(text)%2 == 0 && IsLowerFixedHex(text, len(text)-2)
}

func IsAddress(text string) bool {
	if len(text) != 42 || text[0] != '0' || text[1] != 'x' {
		return false
	}
	for i := 2; i < len(text); i++ {
		c := text[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func IsHexByte(text string) bool {
	return (len(text) == 3 || len(text) == 4) && IsLowerFixedHex(text, len(text)-2)
}

func IsStorageKey(text string) bool {
	return len(text) <= 66 && IsLowerFixedHex(text, len(text)-2)
}

func IsCanonicalDecimal(text string) bool {
	if text == "0" {
		return true
	}
	if len(text) == 0 || text[0] < '1' || text[0] > '9' {
		return false
	}
	for i := 1; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}
