/*
Package jsonrpc2 implements a JSON-RPC 2.0 ClientCodec and ServerCodec
for the net/rpc package and HTTP transport for JSON-RPC 2.0.


RPC method's signature

JSON-RPC 2.0 support positional and named parameters. Which one should be
used when calling server's method depends on type of that method's first
parameter: if it is an Array or Slice then positional parameters should be
used, if it is a Map or Struct then named parameters should be used. (Also
any method can be called without parameters at all.) If first parameter
will be of custom type with json.Unmarshaler interface then it depends on
what is supported by that type - this way you can even implement method
which can be called both with positional and named parameters.

JSON-RPC 2.0 support result of any type, so method's result (second param)
can be a reference of any type supported by json.Marshal.

JSON-RPC 2.0 support error codes and optional extra error data in addition
to error message. If method returns error of standard error type (i.e.
just error message without error code) then error code -32000 will be
used. To define custom error code (and optionally extra error data) method
should return jsonrpc2.Error.


Using positional parameters of different types

If you'll have to provide method which should be called using positional
parameters of different types then it's recommended to implement this
using first parameter of custom type with json.Unmarshaler interface.

To call such a method you'll have to use client.Call() with []interface{}
in args.


Using context to provide transport-level details with parameters

If you want to have access to transport-level details (or any other
request context data) in RPC method then first parameter of that RPC
method should provide WithContext interface. (If first parameter is a
struct then you can just embed Ctx into your struct as shown in example.)

This way you can get access to client IP address or details of client HTTP
request etc. in RPC method.


Decoding errors on client

Because of net/rpc limitations client.Call() can't return JSON-RPC 2.0
error with code, message and extra data - it'll return either one of
rpc.ErrShutdown or io.ErrUnexpectedEOF errors, or encoded JSON-RPC 2.0
error, which have to be decoded using jsonrpc2.ServerError to get error's
code, message and extra data.


Limitations

HTTP client&server does not support Pipelined Requests/Responses.

HTTP client&server does not support GET Request.

HTTP client does not support Batch Request.

Because of net/rpc limitations RPC method MUST NOT return standard
error which begins with '{' and ends with '}'.

Current implementation does a lot of sanity checks to conform to
protocol spec. Making most of them optional may improve performance.
*/
package jsonrpc2
