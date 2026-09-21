// Package refcontext carries reference evidence within one prepared read.
package refcontext

import (
	"context"
	"ethguard/rpc"
	"sync"
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
		copy := ref.Clone()
		c.mu.Lock()
		c.value = &copy
		c.mu.Unlock()
	}
}
func (c *Capture) Reference() *rpc.ReferenceEvidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == nil {
		return nil
	}
	copy := c.value.Clone()
	return &copy
}

type corroborationRequiredKey struct{}

type membersKey struct{}
type corroborationKey struct{}

func WithMembers(ctx context.Context, members any) context.Context {
	return context.WithValue(ctx, membersKey{}, members)
}
func Members(ctx context.Context) (any, bool) {
	v := ctx.Value(membersKey{})
	return v, v != nil
}
func WithCorroboration(ctx context.Context, value any) context.Context {
	return context.WithValue(ctx, corroborationKey{}, value)
}
func Corroboration(ctx context.Context) (any, bool) {
	v := ctx.Value(corroborationKey{})
	return v, v != nil
}

// RequireCorroborated marks typed calls that cannot expose a Selected decision.
func RequireCorroborated(ctx context.Context) context.Context {
	return context.WithValue(ctx, corroborationRequiredKey{}, true)
}

type forwardedKey struct{}

// AllowForwarded marks an intentional single-source call as eligible to
// return locally validated data with Forwarded status.
func WithForwarded(ctx context.Context) context.Context {
	return context.WithValue(ctx, forwardedKey{}, true)
}
func AllowForwarded(ctx context.Context) bool {
	value, _ := ctx.Value(forwardedKey{}).(bool)
	return value
}
func CorroborationRequired(ctx context.Context) bool {
	value, _ := ctx.Value(corroborationRequiredKey{}).(bool)
	return value
}
