package mariadb

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pokemaplx/TrafficLight/internal/pogo"
	"github.com/pokemaplx/TrafficLight/internal/web"
)

func TestProtosSurviveTheBlob(t *testing.T) {
	protos := []pogo.RawProto{
		{Method: 106, Request: []byte("request"), Response: bytes.Repeat([]byte{0, 1, 255}, 5000)},
		{Method: -3, Request: nil, Response: []byte{}},
		{Method: 1 << 40, Request: []byte{0}, Response: nil},
	}
	decoded, err := decodeProtos(encodeProtos(protos))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(protos) {
		t.Fatalf("%d protos came back instead of %d", len(decoded), len(protos))
	}
	for i := range protos {
		if decoded[i].Method != protos[i].Method || !bytes.Equal(decoded[i].Request, protos[i].Request) || !bytes.Equal(decoded[i].Response, protos[i].Response) {
			t.Errorf("proto %d came back as %d %x %x", i, decoded[i].Method, decoded[i].Request, decoded[i].Response)
		}
	}
	if cap(decoded[0].Request) != len(decoded[0].Request) {
		t.Error("a proto can be appended to, into the next one")
	}

	if none, err := decodeProtos(encodeProtos(nil)); err != nil || len(none) != 0 {
		t.Errorf("a request without protos came back as %v, %v", none, err)
	}
}

func TestABrokenBlobIsRefused(t *testing.T) {
	blob := encodeProtos([]pogo.RawProto{{Method: 2, Request: []byte("abc"), Response: []byte("defgh")}})
	for cut := 0; cut < len(blob); cut++ {
		// cut right after a whole proto it is just a shorter list, anywhere else it is broken
		if cut == 1 {
			continue
		}
		if _, err := decodeProtos(blob[:cut]); err == nil {
			t.Errorf("a blob cut off after %d bytes is taken", cut)
		}
	}
	if _, err := decodeProtos(append([]byte{99}, blob[1:]...)); err == nil {
		t.Error("a blob of another version is taken")
	}
}

func TestDatabaseURLs(t *testing.T) {
	for url, want := range map[string]string{
		"mariadb://traffic:s3cret@db:3307/trafficlight":     "traffic:s3cret@tcp(db:3307)/trafficlight",
		"mysql://traffic:s3cret@db/trafficlight":            "traffic:s3cret@tcp(db:3306)/trafficlight",
		"mariadb://traffic:p%40ss%2Fword@[::1]/tl":          "traffic:p@ss/word@tcp([::1]:3306)/tl",
		"mariadb://traffic@db/trafficlight?tls=skip-verify": "traffic@tcp(db:3306)/trafficlight?tls=skip-verify",
		"traffic:s3cret@tcp(db:3306)/trafficlight":          "traffic:s3cret@tcp(db:3306)/trafficlight",
	} {
		config, err := parse(url)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		got := config.User + ":" + config.Passwd + "@" + config.Net + "(" + config.Addr + ")/" + config.DBName
		if config.Passwd == "" {
			got = config.User + "@" + config.Net + "(" + config.Addr + ")/" + config.DBName
		}
		if config.TLSConfig != "" {
			got += "?tls=" + config.TLSConfig
		}
		if got != want {
			t.Errorf("%s: read as %s, want %s", url, got, want)
		}
		if config.Timeout == 0 || config.ReadTimeout == 0 || config.WriteTimeout == 0 {
			t.Errorf("%s: a query could hang forever", url)
		}
	}
}

func TestABadDatabaseURLNeverShowsThePassword(t *testing.T) {
	for _, url := range []string{
		"postgres://traffic:hunter2@db/trafficlight",
		"mariadb://traffic:hunter2@db",
		"mariadb://traffic:hunter2@/trafficlight",
		"mariadb://traffic:hunter2@db:port/trafficlight",
		"traffic:hunter2@db/trafficlight",
		"hunter2",
	} {
		_, err := parse(url)
		if err == nil {
			t.Errorf("%s is taken", url)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error shows the password: %v", url, err)
		}
	}
}

// Runs against a real database if TRAFFICLIGHT_TEST_DATABASE_URL names one, i.e.
//
//	docker run --rm -p 3306:3306 -e MARIADB_ROOT_PASSWORD=test -e MARIADB_DATABASE=trafficlight mariadb
//	TRAFFICLIGHT_TEST_DATABASE_URL=mariadb://root:test@127.0.0.1/trafficlight go test ./internal/mariadb
func TestAgainstADatabase(t *testing.T) {
	url := os.Getenv("TRAFFICLIGHT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TRAFFICLIGHT_TEST_DATABASE_URL is not set")
	}
	archive, err := Open(url)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()

	before, err := archive.Load()
	if err != nil {
		t.Fatal(err)
	}

	handle := int64(9)
	now := time.Now().UnixMilli()
	first := web.Archived{Time: now, Request: pogo.Request{RPCID: 5, RPCStatus: 1, RPCHandle: &handle, Protos: []pogo.RawProto{
		{Method: 106, Request: []byte("request"), Response: bytes.Repeat([]byte{0, 200}, 300_000)},
		{Method: 2},
	}}}
	second := web.Archived{Time: now + 1, Request: pogo.Request{RPCID: 6, Protos: []pogo.RawProto{{Method: 104, Request: []byte{1}}}}}

	firstKey, err := archive.Save(first)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := archive.Save(second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { archive.Delete([]int64{firstKey, secondKey}) })
	if firstKey == 0 || firstKey == secondKey {
		t.Fatalf("keys are %d and %d", firstKey, secondKey)
	}

	loaded, err := archive.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(before)+2 {
		t.Fatalf("%d saved requests, expected %d", len(loaded), len(before)+2)
	}
	got := loaded[len(loaded)-2]
	if got.Key != firstKey || got.Time != now || got.Request.RPCID != 5 || got.Request.RPCStatus != 1 ||
		got.Request.RPCHandle == nil || *got.Request.RPCHandle != 9 || len(got.Request.Protos) != 2 ||
		!bytes.Equal(got.Request.Protos[0].Response, first.Request.Protos[0].Response) {
		t.Errorf("the first request came back as %+v", got.Request.RPCID)
	}
	if last := loaded[len(loaded)-1]; last.Key != secondKey || last.Request.RPCHandle != nil {
		t.Errorf("the second request came back as key %d, handle %v", last.Key, last.Request.RPCHandle)
	}

	if err := archive.Delete([]int64{firstKey, secondKey}); err != nil {
		t.Fatal(err)
	}
	after, err := archive.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("%d saved requests are left, expected %d", len(after), len(before))
	}
}
