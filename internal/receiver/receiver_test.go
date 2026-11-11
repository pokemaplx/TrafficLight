package receiver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
)

func post(body string) (*httptest.ResponseRecorder, []pogo.Request, []string) {
	var requests []pogo.Request
	var forwarded []string
	receiver := &Receiver{
		Forward: func(body []byte) { forwarded = append(forwarded, string(body)) },
		Sink:    func(request pogo.Request) { requests = append(requests, request) },
	}
	recorder := httptest.NewRecorder()
	receiver.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return recorder, requests, forwarded
}

func TestEveryShapeMitmsSendIsTaken(t *testing.T) {
	// "aGVsbG8=" is hello
	for name, body := range map[string]string{
		"traffic light's own": `{"rpcid":7,"rpcstatus":1,"rpchandle":3,"protos":[{"method":106,"request":"aGVsbG8=","response":"aGVsbG8="}]}`,
		"golbat's":            `{"username":"a","rpcid":7,"rpcstatus":1,"rpchandle":3,"contents":[{"type":106,"request":"aGVsbG8=","payload":"aGVsbG8="}]}`,
		"a list":              `[{"rpcid":"7","rpcstatus":1.0,"rpchandle":3,"contents":[{"type":"106","request":"aGVsbG8=","payload":"aGVsbG8="}]}]`,
		"sloppy base64":       `{"rpcid":7,"rpcstatus":1,"rpchandle":3,"protos":[{"method":106,"request":"aGVs\nbG8","response":"aGVsbG8=\u0000\u0000"}]}`,
	} {
		recorder, requests, forwarded := post(body)
		if recorder.Code != http.StatusOK || len(requests) != 1 {
			t.Errorf("%s: status %d, %d requests: %s", name, recorder.Code, len(requests), recorder.Body)
			continue
		}
		request := requests[0]
		if request.RPCID != 7 || request.RPCStatus != 1 || request.RPCHandle == nil || *request.RPCHandle != 3 {
			t.Errorf("%s: the rpc is read as %+v", name, request)
		}
		proto := request.Protos[0]
		if proto.Method != 106 || string(proto.Request) != "hello" || string(proto.Response) != "hello" {
			t.Errorf("%s: the proto is read as %d %q %q", name, proto.Method, proto.Request, proto.Response)
		}
		if len(forwarded) != 1 || forwarded[0] != body {
			t.Errorf("%s: the body isn't forwarded untouched", name)
		}
	}
}

func TestWhatIsMissingIsEmpty(t *testing.T) {
	recorder, requests, _ := post(`{"protos":[{"method":2,"request":null}]}`)
	if recorder.Code != http.StatusOK || len(requests) != 1 {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	request := requests[0]
	if request.RPCID != 0 || request.RPCHandle != nil || len(request.Protos[0].Request) != 0 || len(request.Protos[0].Response) != 0 {
		t.Errorf("read as %+v", request)
	}
}

func TestAnEmptyListIsFine(t *testing.T) {
	// what the healthcheck posts
	recorder, requests, _ := post(`[]`)
	if recorder.Code != http.StatusOK || len(requests) != 0 {
		t.Errorf("status %d, %d requests", recorder.Code, len(requests))
	}
}

func TestBadRequestsAreRefused(t *testing.T) {
	for body, forward := range map[string]bool{
		`{"rpcid":`:                               false,
		`{"rpcid":1}`:                             true,
		`{"protos":[{"request":"aGVsbG8="}]}`:     true,
		`{"protos":[{"method":1,"request":"A"}]}`: true,
		`{"protos":"nope"}`:                       true,
		`5`:                                       true,
	} {
		recorder, requests, forwarded := post(body)
		if recorder.Code != http.StatusBadRequest || len(requests) != 0 {
			t.Errorf("%s: status %d, %d requests", body, recorder.Code, len(requests))
		}
		// what is json goes on anyway: whether Traffic Light understands it is not the
		// business of whoever it forwards to
		if (len(forwarded) == 1) != forward {
			t.Errorf("%s: forwarded %d times", body, len(forwarded))
		}
	}
}

func TestProtosShareOneAllocation(t *testing.T) {
	_, requests, _ := post(`{"protos":[{"method":1,"request":"aGVsbG8=","response":"d29ybGQ="},{"method":2,"request":"Zm9v","response":"YmFy"}]}`)
	protos := requests[0].Protos
	joined := string(protos[0].Request) + string(protos[0].Response) + string(protos[1].Request) + string(protos[1].Response)
	if joined != "helloworldfoobar" {
		t.Fatalf("read as %q", joined)
	}
	// none of them can grow into its neighbour
	if cap(protos[0].Request) != 5 || cap(protos[1].Response) != 3 {
		t.Errorf("parts have room to grow: %d, %d", cap(protos[0].Request), cap(protos[1].Response))
	}
}

func TestOnlyPostsToTheRootAreTaken(t *testing.T) {
	receiver := &Receiver{Sink: func(pogo.Request) {}}
	recorder := httptest.NewRecorder()
	receiver.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /: %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	receiver.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/raw", strings.NewReader("[]")))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("POST /raw: %d", recorder.Code)
	}
}
