package ethcodec

import "regexp"

// These exact schema patterns are shared by generated constructors and
// runtime scalar consumers. Compatibility never runs inside these predicates.
const (
	QuantityPattern      = `^0x(0|[1-9a-f][0-9a-f]*)$`
	DataPattern          = `^0x([0-9a-f]{2})*$`
	DecimalStringPattern = `^(0|[1-9][0-9]*)$`
)

var (
	strictQuantity = regexp.MustCompile(QuantityPattern)
	strictData     = regexp.MustCompile(DataPattern)
	strictDecimal  = regexp.MustCompile(DecimalStringPattern)
)

func IsCanonicalQuantity(text string) bool { return strictQuantity.MatchString(text) }
func IsWholeByteData(text string) bool     { return strictData.MatchString(text) }
func IsCanonicalDecimal(text string) bool  { return strictDecimal.MatchString(text) }
