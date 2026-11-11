package receiver

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

// body is what a mitm posts for one big response, about 190 KB of json
func body() []byte {
	payload := make([]byte, 143_000)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	return []byte(`{"rpcid":1,"rpcstatus":1,"rpchandle":5,"protos":[{"method":106,"request":"","response":"` +
		base64.StdEncoding.EncodeToString(payload) + `"}]}`)
}

// What reading a request costs, without the network
func BenchmarkParse(b *testing.B) {
	data := body()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

// What taking a request costs, from the http handler on
func BenchmarkReceive(b *testing.B) {
	data := body()
	receiver := &Receiver{Sink: func(pogo.Request) {}}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
		receiver.ServeHTTP(httptest.NewRecorder(), request)
	}
}
