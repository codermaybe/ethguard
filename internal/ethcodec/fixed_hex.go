package ethcodec

// IsLowerFixedHex reports whether text is exactly a lowercase "0x" prefix
// followed by hexDigits ASCII digits from 0-9 or a-f. It checks an already
// decoded string and does not parse JSON or normalize its spelling.
func IsLowerFixedHex(text string, hexDigits int) bool {
	if hexDigits < 0 || len(text) < 2 || len(text)-2 != hexDigits || text[0] != '0' || text[1] != 'x' {
		return false
	}
	for i := 2; i < len(text); i++ {
		c := text[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
