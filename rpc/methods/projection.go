package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/projection"
)

// StandardField and Projection remain aliases for compatibility. The
// implementation lives in rpc/internal/projection so method packages only
// declare their method-specific fields.
type StandardField = projection.StandardField
type Projection = projection.Projection

func NewStandardProjector(fields []StandardField) rpc.ObservationProjector {
	return projection.NewStandardProjector(fields)
}

func ProjectStandard(observation rpc.CommonObservation, fields []StandardField) Projection {
	return projection.ProjectStandard(observation, fields)
}

func TransactionReceiptStandardFields() []StandardField {
	return projection.TransactionReceiptStandardFields()
}

func BlockStandardFields() []StandardField {
	return projection.BlockStandardFields()
}

func TransactionStandardFields() []StandardField {
	return projection.TransactionStandardFields()
}

func LogsStandardFields() []StandardField {
	return projection.LogsStandardFields()
}

func FeeHistoryStandardFields() []StandardField {
	return projection.FeeHistoryStandardFields()
}

func ScalarStandardProjector() rpc.ObservationProjector {
	return projection.ScalarStandardProjector()
}
