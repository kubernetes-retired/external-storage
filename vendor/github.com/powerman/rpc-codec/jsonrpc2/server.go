// Based on net/rpc/jsonrpc by:
// Copyright 2010 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/rpc"
	"sync"
)

type serverCodec struct {
	encmutex sync.Mutex    // protects enc
	dec      *json.Decoder // for reading JSON values
	enc      *json.Encoder // for writing JSON values
	c        io.Closer
	srv      *rpc.Server
	ctx      context.Context

	// temporary work space
	req serverRequest

	// JSON-RPC clients can use arbitrary json values as request IDs.
	// Package rpc expects uint64 request IDs.
	// We assign uint64 sequence numbers to incoming requests
	// but save the original request ID in the pending map.
	// When rpc responds, we use the sequence number in
	// the response to find the original request ID.
	mutex   sync.Mutex // protects seq, pending
	seq     uint64
	pending map[uint64]*json.RawMessage
}

// NewServerCodec returns a new rpc.ServerCodec using JSON-RPC 2.0 on conn,
// which will use srv to execute batch requests.
//
// If srv is nil then rpc.DefaultServer will be used.
//
// For most use cases NewServerCodec is too low-level and you should use
// ServeConn instead. You'll need NewServerCodec if you wanna register
// your own object of type named "JSONRPC2" (same as used internally to
// process batch requests) or you wanna use custom rpc server object
// instead of rpc.DefaultServer to process requests on conn.
func NewServerCodec(conn io.ReadWriteCloser, srv *rpc.Server) rpc.ServerCodec {
	if srv == nil {
		srv = rpc.DefaultServer
	}
	srv.Register(JSONRPC2{})
	return &serverCodec{
		dec:     json.NewDecoder(conn),
		enc:     json.NewEncoder(conn),
		c:       conn,
		srv:     srv,
		ctx:     context.Background(),
		pending: make(map[uint64]*json.RawMessage),
	}
}

// NewServerCodecContext is NewServerCodec with given context provided
// within parameters for compatible RPC methods.
func NewServerCodecContext(ctx context.Context, conn io.ReadWriteCloser, srv *rpc.Server) rpc.ServerCodec {
	codec := NewServerCodec(conn, srv)
	codec.(*serverCodec).ctx = ctx
	return codec
}

type serverRequest struct {
	Version string           `json:"jsonrpc"`
	Method  string           `json:"method"`
	Params  *json.RawMessage `json:"params"`
	ID      *json.RawMessage `json:"id"`
}

func (r *serverRequest) reset() {
	r.Version = ""
	r.Method = ""
	r.Params = nil
	r.ID = nil
}

func (r *serverRequest) UnmarshalJSON(raw []byte) error {
	r.reset()
	type req *serverRequest
	if err := json.Unmarshal(raw, req(r)); err != nil {
		return errors.New("bad request")
	}

	var o = make(map[string]*json.RawMessage)
	if err := json.Unmarshal(raw, &o); err != nil {
		return errors.New("bad request")
	}
	if o["jsonrpc"] == nil || o["method"] == nil {
		return errors.New("bad request")
	}
	_, okID := o["id"]
	_, okParams := o["params"]
	if len(o) == 3 && !(okID || okParams) || len(o) == 4 && !(okID && okParams) || len(o) > 4 {
		return errors.New("bad request")
	}
	if r.Version != "2.0" {
		return errors.New("bad request")
	}
	if okParams {
		if r.Params == nil || len(*r.Params) == 0 {
			return errors.New("bad request")
		}
		switch []byte(*r.Params)[0] {
		case '[', '{':
		default:
			return errors.New("bad request")
		}
	}
	if okID && r.ID == nil {
		r.ID = &null
	}
	if okID {
		if len(*r.ID) == 0 {
			return errors.New("bad request")
		}
		switch []byte(*r.ID)[0] {
		case 't', 'f', '{', '[':
			return errors.New("bad request")
		}
	}

	return nil
}

type serverResponse struct {
	Version string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  interface{}      `json:"result,omitempty"`
	Error   interface{}      `json:"error,omitempty"`
}

func (c *serverCodec) ReadRequestHeader(r *rpc.Request) (err error) {
	// If return error:
	// - codec will be closed
	// So, try to send error reply to client before returning error.
	var raw json.RawMessage
	if err := c.dec.Decode(&raw); err != nil {
		c.encmutex.Lock()
		c.enc.Encode(serverResponse{Version: "2.0", ID: &null, Error: errParse})
		c.encmutex.Unlock()
		return err
	}

	if len(raw) > 0 && raw[0] == '[' {
		c.req.Version = "2.0"
		c.req.Method = "JSONRPC2.Batch"
		c.req.Params = &raw
		c.req.ID = &null
	} else if err := json.Unmarshal(raw, &c.req); err != nil {
		if err.Error() == "bad request" {
			c.encmutex.Lock()
			c.enc.Encode(serverResponse{Version: "2.0", ID: &null, Error: errRequest})
			c.encmutex.Unlock()
		}
		return err
	}

	r.ServiceMethod = c.req.Method

	// JSON request id can be any JSON value;
	// RPC package expects uint64.  Translate to
	// internal uint64 and save JSON on the side.
	c.mutex.Lock()
	c.seq++
	c.pending[c.seq] = c.req.ID
	c.req.ID = nil
	r.Seq = c.seq
	c.mutex.Unlock()

	return nil
}

func (c *serverCodec) ReadRequestBody(x interface{}) error {
	// If x!=nil and return error e:
	// - WriteResponse() will be called with e.Error() in r.Error
	if x == nil {
		return nil
	}
	if x, ok := x.(WithContext); ok {
		x.SetContext(c.ctx)
	}
	if c.req.Params == nil {
		return nil
	}
	if c.req.Method == "JSONRPC2.Batch" {
		arg := x.(*BatchArg)
		arg.srv = c.srv
		if err := json.Unmarshal(*c.req.Params, &arg.reqs); err != nil {
			return NewError(errParams.Code, err.Error())
		}
		if len(arg.reqs) == 0 {
			return errRequest
		}
	} else if err := json.Unmarshal(*c.req.Params, x); err != nil {
		return NewError(errParams.Code, err.Error())
	}
	return nil
}

var null = json.RawMessage([]byte("null"))

func (c *serverCodec) WriteResponse(r *rpc.Response, x interface{}) error {
	// If return error: nothing happens.
	// In r.Error will be "" or .Error() of error returned by:
	// - ReadRequestBody()
	// - called RPC method
	c.mutex.Lock()
	b, ok := c.pending[r.Seq]
	if !ok {
		c.mutex.Unlock()
		return errors.New("invalid sequence number in response")
	}
	delete(c.pending, r.Seq)
	c.mutex.Unlock()

	if replies, ok := x.(*[]*json.RawMessage); r.ServiceMethod == "JSONRPC2.Batch" && ok {
		if len(*replies) == 0 {
			return nil
		}
		c.encmutex.Lock()
		defer c.encmutex.Unlock()
		return c.enc.Encode(replies)
	}

	if b == nil {
		// Notification. Do not respond.
		return nil
	}
	resp := serverResponse{Version: "2.0", ID: b}
	if r.Error == "" {
		if x == nil {
			resp.Result = &null
		} else {
			resp.Result = x
		}
	} else if r.Error[0] == '{' && r.Error[len(r.Error)-1] == '}' {
		// Well… this check for '{'…'}' isn't too strict, but I
		// suppose we're trusting our own RPC methods (this way they
		// can force sending wrong reply or many replies instead
		// of one) and normal errors won't be formatted this way.
		raw := json.RawMessage(r.Error)
		resp.Error = &raw
	} else {
		raw := json.RawMessage(newError(r.Error).Error())
		resp.Error = &raw
	}
	c.encmutex.Lock()
	defer c.encmutex.Unlock()
	return c.enc.Encode(resp)
}

func (c *serverCodec) Close() error {
	return c.c.Close()
}

// ServeConn runs the JSON-RPC 2.0 server on a single connection.
// ServeConn blocks, serving the connection until the client hangs up.
// The caller typically invokes ServeConn in a go statement.
func ServeConn(conn io.ReadWriteCloser) {
	rpc.ServeCodec(NewServerCodec(conn, nil))
}

// ServeConnContext is ServeConn with given context provided
// within parameters for compatible RPC methods.
func ServeConnContext(ctx context.Context, conn io.ReadWriteCloser) {
	rpc.ServeCodec(NewServerCodecContext(ctx, conn, nil))
}
