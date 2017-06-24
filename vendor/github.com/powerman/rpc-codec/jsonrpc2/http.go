package jsonrpc2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/rpc"
)

const contentType = "application/json"

type contextKey int

var httpRequestContextKey contextKey = 0

// HTTPRequestFromContext returns HTTP request related to this RPC (if
// you use HTTPHander to serve JSON RPC 2.0 over HTTP) or nil otherwise.
func HTTPRequestFromContext(ctx context.Context) *http.Request {
	req, _ := ctx.Value(httpRequestContextKey).(*http.Request)
	return req
}

type httpServerConn struct {
	req     io.Reader
	res     io.Writer
	replied bool
}

func (conn *httpServerConn) Read(buf []byte) (int, error) {
	return conn.req.Read(buf)
}

func (conn *httpServerConn) Write(buf []byte) (int, error) {
	conn.replied = true
	return conn.res.Write(buf)
}

func (conn *httpServerConn) Close() error {
	return nil
}

type httpHandler struct {
	rpc *rpc.Server
}

// HTTPHandler returns handler for HTTP requests which will execute
// incoming JSON-RPC 2.0 over HTTP using srv.
//
// If srv is nil then rpc.DefaultServer will be used.
//
// Specification: http://www.simple-is-better.org/json-rpc/transport_http.html
func HTTPHandler(srv *rpc.Server) http.Handler {
	if srv == nil {
		srv = rpc.DefaultServer
	}
	return &httpHandler{srv}
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", contentType)

	if req.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if mediaType != contentType || req.Header.Get("Accept") != contentType {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	ctx := context.WithValue(context.Background(), httpRequestContextKey, req)
	conn := &httpServerConn{req: req.Body, res: w}
	h.rpc.ServeRequest(NewServerCodecContext(ctx, conn, h.rpc))
	if !conn.replied {
		w.WriteHeader(http.StatusNoContent)
	}
}

// Doer is an interface for doing HTTP requests.
type Doer interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

// The DoerFunc type is an adapter to allow the use of ordinary
// functions as HTTP clients. If f is a function with the appropriate
// signature, DoerFunc(f) is a client that calls f.
type DoerFunc func(req *http.Request) (resp *http.Response, err error)

// DoerFunc calls f(req).
func (f DoerFunc) Do(req *http.Request) (resp *http.Response, err error) {
	return f(req)
}

type httpClientConn struct {
	url   string
	doer  Doer
	ready chan io.ReadCloser
	body  io.ReadCloser
}

func (conn *httpClientConn) Read(buf []byte) (int, error) {
	if conn.body == nil {
		conn.body = <-conn.ready
	}
	n, err := conn.body.Read(buf)
	if err == io.EOF {
		conn.body.Close()
		conn.body = nil
		err = nil
		if n == 0 {
			return conn.Read(buf)
		}
	}
	return n, err
}

func (conn *httpClientConn) Write(buf []byte) (int, error) {
	b := make([]byte, len(buf))
	copy(b, buf)
	go func() {
		req, err := http.NewRequest("POST", conn.url, bytes.NewReader(b))
		if err == nil {
			req.Header.Add("Content-Type", contentType)
			req.Header.Add("Accept", contentType)
			var resp *http.Response
			resp, err = conn.doer.Do(req)
			const maxBodySlurpSize = 32 * 1024
			if err != nil {
			} else if resp.Header.Get("Content-Type") != contentType {
				err = fmt.Errorf("bad HTTP Content-Type: %s", resp.Header.Get("Content-Type"))
			} else if resp.StatusCode == http.StatusOK {
				conn.ready <- resp.Body
				return
			} else if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusAccepted {
				// Read the body if small so underlying TCP connection will be re-used.
				// No need to check for errors: if it fails, Transport won't reuse it anyway.
				if resp.ContentLength == -1 || resp.ContentLength <= maxBodySlurpSize {
					io.CopyN(ioutil.Discard, resp.Body, maxBodySlurpSize)
				}
				resp.Body.Close()
				return
			} else {
				err = fmt.Errorf("bad HTTP Status: %s", resp.Status)
			}
			if resp != nil {
				// Read the body if small so underlying TCP connection will be re-used.
				// No need to check for errors: if it fails, Transport won't reuse it anyway.
				if resp.ContentLength == -1 || resp.ContentLength <= maxBodySlurpSize {
					io.CopyN(ioutil.Discard, resp.Body, maxBodySlurpSize)
				}
				resp.Body.Close()
			}
		}
		var res clientResponse
		if json.Unmarshal(b, &res) == nil && res.ID == nil {
			return // ignore error from Notification
		}
		res.Error = NewError(errInternal.Code, err.Error())
		buf := &bytes.Buffer{}
		json.NewEncoder(buf).Encode(res)
		conn.ready <- ioutil.NopCloser(buf)
	}()
	return len(buf), nil
}

func (conn *httpClientConn) Close() error {
	return nil
}

// NewHTTPClient returns a new Client to handle requests to the
// set of services at the given url.
func NewHTTPClient(url string) *Client {
	return NewCustomHTTPClient(url, nil)
}

// NewCustomHTTPClient returns a new Client to handle requests to the
// set of services at the given url using provided doer (&http.Client{} by
// default).
//
// Use doer to customize HTTP authorization/headers/etc. sent with each
// request (it method Do() will receive already configured POST request
// with url, all required headers and body set according to specification).
func NewCustomHTTPClient(url string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{}
	}
	return NewClient(&httpClientConn{
		url:   url,
		doer:  doer,
		ready: make(chan io.ReadCloser, 16),
	})
}
