package jsonrpc2

import "context"

// WithContext is an interface which should be implemented by RPC method
// parameters type if you need access to request context in RPC method.
//
// Request context will be same as was provided to corresponding
// ServeConnContext/NewServerCodecContext or context.Background otherwise.
type WithContext interface {
	Context() context.Context
	SetContext(ctx context.Context)
}

// Ctx can be embedded into your struct with RPC method parameters (if
// that method parameters type is a struct) to make it implement
// WithContext interface and thus have access to request context.
type Ctx struct {
	ctx context.Context
}

// Context returns ctx given to preceding SetContext call.
func (c *Ctx) Context() context.Context {
	return c.ctx
}

// SetContext saves ctx for succeeding Context calls.
func (c *Ctx) SetContext(ctx context.Context) {
	c.ctx = ctx
}
