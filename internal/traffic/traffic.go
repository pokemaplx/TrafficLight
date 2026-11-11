// Package traffic is what a mitm sends, the way it sends it: still encoded.
//
// Everything else passes these around. The receiver reads them from a request, the outputs
// keep or print them, and decode makes sense of them when somebody looks.
package traffic

// RawProto is one method call: its number, and the bytes of its request and its response
type RawProto struct {
	Method   int64
	Request  []byte
	Response []byte
}

// Request is everything a mitm sends about one rpc
type Request struct {
	RPCID     int64
	RPCStatus int64
	RPCHandle *int64
	Protos    []RawProto
}
