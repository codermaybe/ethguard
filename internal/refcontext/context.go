// Package refcontext carries reference evidence within one prepared read.
package refcontext

import (
	"context"
	"sync"

	"github.com/codermaybe/ethguard/rpc"
)

type key struct{}
type Capture struct {
	mu    sync.Mutex
	value *rpc.ReferenceEvidence
}

func New(ctx context.Context) (context.Context, *Capture) {
	c := new(Capture)
	return context.WithValue(ctx, key{}, c), c
}
func Publish(ctx context.Context, ref rpc.ReferenceEvidence) {
	if c, ok := ctx.Value(key{}).(*Capture); ok {
		clone := ref.Clone()
		c.mu.Lock()
		c.value = &clone
		c.mu.Unlock()
	}
}
func (c *Capture) Reference() *rpc.ReferenceEvidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == nil {
		return nil
	}
	clone := c.value.Clone()
	return &clone
}

// invocationKey carries the in-flight invocation value. It is the
// transitional carrier until every establishment path takes the invocation
// explicitly; new code should prefer the invocation parameter.
type invocationKey struct{}

func WithInvocation(ctx context.Context, invocation rpc.Invocation) context.Context {
	return context.WithValue(ctx, invocationKey{}, invocation)
}

func Invocation(ctx context.Context) (rpc.Invocation, bool) {
	invocation, ok := ctx.Value(invocationKey{}).(rpc.Invocation)
	return invocation, ok
}
