package runtime

import (
	"context"
	"time"

	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/rpc"
)

// capturedHeadReader records finalized reference rounds on the invocation's
// raw trail. It is a runtime-side adapter: reference.Finalized keeps its
// HeadReader signature and algorithm, and the capture stays scoped to the
// invocation that owns the stage.
type capturedHeadReader struct {
	inner      reference.HeadReader
	invocation rpc.Invocation
}

func (r capturedHeadReader) Read(ctx context.Context, tag string, timeout time.Duration) ([]reference.HeadObservation, error) {
	observations, err := r.inner.Read(ctx, tag, timeout)
	captureHeadObservations(r.invocation, observations, err)
	return observations, err
}

func (r capturedHeadReader) Confirm(ctx context.Context, height uint64, sourceIDs []string, timeout time.Duration) ([]reference.HeadObservation, error) {
	observations, err := r.inner.Confirm(ctx, height, sourceIDs, timeout)
	captureHeadObservations(r.invocation, observations, err)
	return observations, err
}

// captureHeadObservations mirrors captureEmission's record shape for the
// finalized algorithm's observation lists: one raw response per observed
// member, plus one collection record for the round's collect error.
func captureHeadObservations(invocation rpc.Invocation, observations []reference.HeadObservation, collectErr error) {
	capture := invocation.Capture()
	if capture == nil {
		return
	}
	for _, observation := range observations {
		errText := ""
		if observation.Err != nil {
			errText = observation.Err.Error()
		}
		capture.Record("response", anchorMethodName, observation.Source, observation.Value, errText)
	}
	if collectErr != nil {
		capture.Record("collect", anchorMethodName, rpc.SourceRef{}, nil, collectErr.Error())
	}
}
