// +build go1.3

package jsonrpc2_test

import (
	"io"
	"net"
	"net/http/httptest"
	"net/rpc"
	"net/rpc/jsonrpc"
	"testing"

	"github.com/powerman/rpc-codec/jsonrpc2"
)

type client interface {
	Call(string, interface{}, interface{}) error
}

func benchmarkRPC(b *testing.B, client client) {
	b.ResetTimer()
	// for n := 0; n < b.N; n++ {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var got int
			err := client.Call("Svc.Sum", [2]int{3, 5}, &got)
			if err != nil {
				b.Errorf("Svc.Sum(3,5), err = %v", err)
			}
			if got != 8 {
				b.Errorf("Svc.Sum(3,5) = %v, want = 8", got)
			}
		}
	})
}

func listen(b *testing.B, serveConn func(conn io.ReadWriteCloser)) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(conn)
		}
	}()
	return ln
}

func BenchmarkJSONRPC2_pipe(b *testing.B) {
	cli, srv := net.Pipe()
	go jsonrpc2.ServeConn(srv)
	client := jsonrpc2.NewClient(cli)
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkJSONRPC_pipe(b *testing.B) {
	cli, srv := net.Pipe()
	go jsonrpc.ServeConn(srv)
	client := jsonrpc.NewClient(cli)
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkGOBRPC_pipe(b *testing.B) {
	cli, srv := net.Pipe()
	go rpc.ServeConn(srv)
	client := rpc.NewClient(cli)
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkJSONRPC2_tcp(b *testing.B) {
	ln := listen(b, jsonrpc2.ServeConn)
	defer ln.Close()
	client, err := jsonrpc2.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkJSONRPC_tcp(b *testing.B) {
	ln := listen(b, jsonrpc.ServeConn)
	defer ln.Close()
	client, err := jsonrpc.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkGOBRPC_tcp(b *testing.B) {
	ln := listen(b, rpc.ServeConn)
	defer ln.Close()
	client, err := rpc.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkJSONRPC2_http(b *testing.B) {
	ts := httptest.NewServer(jsonrpc2.HTTPHandler(nil))
	// Don't close because of https://github.com/golang/go/issues/12262
	// defer ts.Close()
	client := jsonrpc2.NewHTTPClient(ts.URL)
	defer client.Close()
	benchmarkRPC(b, client)
}

func BenchmarkGOBRPC_http(b *testing.B) {
	ts := httptest.NewServer(rpc.DefaultServer)
	// Don't close because of https://github.com/golang/go/issues/12262
	// defer ts.Close()
	client, err := rpc.DialHTTP("tcp", ts.URL[7:])
	if err != nil {
		b.Errorf("rpc.DialHTTP(tcp, %q), err = %v", ts.URL, err)
	}
	defer client.Close()
	benchmarkRPC(b, client)
}
