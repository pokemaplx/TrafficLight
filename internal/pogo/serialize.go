package pogo

import (
	"encoding/base64"
	"strconv"
	"sync"
)

// The json the web UI works with. All of it is appended to a buffer by hand: these run for
// every record of the log whenever a browser connects, and the text of a record can be a
// megabyte.

var scratchPool = sync.Pool{New: func() any { return new([]byte) }}

// buffers that grew for a huge message go back to the garbage collector instead of the pool
const maxPooledScratch = 1 << 20

// AppendDetail appends everything the inspector shows about a proto
func AppendDetail(dst []byte, proto *Proto) []byte {
	dst = append(dst, `{"method":`...)
	dst = appendName(dst, proto.MethodName)
	dst = append(dst, `,"value":`...)
	dst = strconv.AppendInt(dst, proto.MethodValue, 10)
	dst = append(dst, `,"request":`...)
	dst = appendMessageDetail(dst, &proto.Request)
	dst = append(dst, `,"response":`...)
	dst = appendMessageDetail(dst, &proto.Response)
	dst = append(dst, `,"proxy":`...)
	if proto.Proxy != nil {
		dst = AppendDetail(dst, proto.Proxy)
	} else {
		dst = append(dst, "null"...)
	}
	return append(dst, '}')
}

func appendMessageDetail(dst []byte, message *Message) []byte {
	dst = appendMessageHead(dst, message, message.State())
	dst = append(dst, `,"base64":"`...)
	dst = base64.StdEncoding.AppendEncode(dst, message.Raw)
	dst = append(dst, `","text":`...)
	dst = appendText(dst, message, false)
	dst = append(dst, `,"json":`...)
	dst = message.AppendJSON(dst)
	dst = append(dst, `,"tree":`...)
	if message.Payload != nil {
		dst = AppendTree(dst, message.Payload)
	} else {
		dst = append(dst, "null"...)
	}
	return append(dst, '}')
}

// AppendExport appends a proto the way mitms send it, so an export can be posted to Traffic
// Light again. "decoded" is for humans
func AppendExport(dst []byte, proto *Proto) []byte {
	dst = append(dst, `{"method":`...)
	dst = strconv.AppendInt(dst, proto.MethodValue, 10)
	dst = append(dst, `,"request":"`...)
	dst = base64.StdEncoding.AppendEncode(dst, proto.Request.Raw)
	dst = append(dst, `","response":"`...)
	dst = base64.StdEncoding.AppendEncode(dst, proto.Response.Raw)
	dst = append(dst, `","decoded":`...)
	dst = appendDecoded(dst, proto)
	return append(dst, '}')
}

func appendDecoded(dst []byte, proto *Proto) []byte {
	dst = append(dst, `{"method":`...)
	dst = appendName(dst, proto.MethodName)
	for _, part := range [2]struct {
		key     string
		message *Message
	}{{`,"request":{"name":`, &proto.Request}, {`,"response":{"name":`, &proto.Response}} {
		dst = append(dst, part.key...)
		dst = appendName(dst, part.message.Name)
		dst = append(dst, `,"data":`...)
		dst = part.message.AppendJSON(dst)
		dst = append(dst, '}')
	}
	dst = append(dst, `,"proxy":`...)
	if proto.Proxy != nil {
		dst = appendDecoded(dst, proto.Proxy)
	} else {
		dst = append(dst, "null"...)
	}
	return append(dst, '}')
}

func appendMessageHead(dst []byte, message *Message, state string) []byte {
	dst = append(dst, `{"name":`...)
	dst = appendName(dst, message.Name)
	dst = append(dst, `,"state":"`...)
	dst = append(dst, state...)
	dst = append(dst, `","size":`...)
	return strconv.AppendInt(dst, int64(len(message.Raw)), 10)
}

// appendText appends a message's text as a json string
func appendText(dst []byte, message *Message, oneLine bool) []byte {
	scratch := scratchPool.Get().(*[]byte)
	text := message.AppendText((*scratch)[:0], oneLine)
	dst = AppendJSONBytes(dst, text)
	if cap(text) <= maxPooledScratch {
		*scratch = text
		scratchPool.Put(scratch)
	}
	return dst
}

// appendName appends a name, or null for the empty one
func appendName(dst []byte, name string) []byte {
	if name == "" {
		return append(dst, "null"...)
	}
	return AppendJSONString(dst, name)
}
