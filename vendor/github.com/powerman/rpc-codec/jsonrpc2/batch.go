package jsonrpc2

import (
	"encoding/json"
	"net"
	"net/rpc"
)

var jErrRequest = json.RawMessage(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Invalid request"}}`)

// JSONRPC2 is an internal RPC service used to process batch requests.
type JSONRPC2 struct{}

// BatchArg is a param for internal RPC JSONRPC2.Batch.
type BatchArg struct {
	srv  *rpc.Server
	reqs []*json.RawMessage
	Ctx
}

// Batch is an internal RPC method used to process batch requests.
func (JSONRPC2) Batch(arg BatchArg, replies *[]*json.RawMessage) (err error) {
	cli, srv := net.Pipe()
	defer cli.Close()
	go arg.srv.ServeCodec(NewServerCodecContext(arg.Context(), srv, arg.srv))

	replyc := make(chan *json.RawMessage, len(arg.reqs))
	donec := make(chan struct{}, 1)

	go func() {
		dec := json.NewDecoder(cli)
		*replies = make([]*json.RawMessage, 0, len(arg.reqs))
		for reply := range replyc {
			if reply != nil {
				*replies = append(*replies, reply)
			} else {
				*replies = append(*replies, new(json.RawMessage))
				if dec.Decode((*replies)[len(*replies)-1]) != nil {
					(*replies)[len(*replies)-1] = &jErrRequest
				}
			}
		}
		donec <- struct{}{}
	}()

	var testreq serverRequest
	for _, req := range arg.reqs {
		if req == nil || json.Unmarshal(*req, &testreq) != nil {
			replyc <- &jErrRequest
		} else {
			if testreq.ID != nil {
				replyc <- nil
			}
			if _, err = cli.Write(append(*req, '\n')); err != nil {
				break
			}
		}
	}

	close(replyc)
	<-donec
	return
}
