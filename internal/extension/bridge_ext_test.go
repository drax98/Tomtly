package extension

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestServiceBridge(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(401)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /devices":
			io.WriteString(w, `[{"id":"ble 1","name":"Thermometer","kind":"sensor","product":"X","online":true,"dps":[
			 {"dp":"7","code":"temp_current","type":"Integer","values":{"unit":"C"},"scale":1,"writable":false,"value":215},
			 {"dp":"9","code":"beep","type":"Boolean","values":{},"writable":true,"value":null}]}]`)
		case "GET /data":
			io.WriteString(w, `{"rssi":-60}`)
		default:
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			posts = append(posts, r.Method+" "+r.URL.EscapedPath()+" "+string(b))
			mu.Unlock()
			w.WriteHeader(202)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	m := startExt(t, example(t, "service-bridge"), map[string]any{"host": host, "token": "s3cret", "data": true})
	eventually(t, "device", func() bool { return len(m.Devices()) == 1 })
	d := m.Devices()[0]
	if d.ID != "service-bridge:ble 1" || d.Kind != "sensor" || !d.Online || len(d.DPs) != 2 || d.DPs[0].Scale != 1 || d.DPs[0].Value != float64(215) {
		t.Fatalf("%+v", d)
	}
	var data []byte
	eventually(t, "data", func() bool {
		data, _, _ = m.plugins.Get("service-bridge")
		return data != nil && string(data) != "null"
	})
	if string(data) != `{"rssi":-60}` {
		t.Fatalf("data %s", data)
	}
	if err := m.Write(d.ID, "beep", true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "post", func() bool { mu.Lock(); defer mu.Unlock(); return len(posts) == 1 })
	if posts[0] != `POST /devices/ble%201/dps/9 {"value":true}` {
		t.Fatalf("%s", posts[0])
	}
	if s := status(m); len(s.Violations) != 0 || len(s.Warnings) != 0 {
		t.Fatalf("%+v", s)
	}

	// A wrong token is explained, not shown as an empty home.
	bad := startExt(t, example(t, "service-bridge"), map[string]any{"host": host, "token": "nope"})
	failsWith(t, bad, "refused the token")
	b, _ := json.Marshal(status(bad))
	if strings.Contains(string(b), "nope") {
		t.Fatal("token leaked")
	}
}

func TestHTTPJSONFetchesWithItsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/today" || r.Header.Get("Authorization") != "Bearer sekret-value" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"price": 42}`))
	}))
	defer server.Close()
	m := run(t, example(t, "http-json"), map[string]any{"host": strings.TrimPrefix(server.URL, "http://"), "path": "/today", "token": "sekret-value"}, allowLoopback)
	eventually(t, "the data", func() bool {
		data, _, _ := m.plugins.Get("http-json")
		return string(data) == `{"price":42}`
	})
	bad := run(t, example(t, "http-json"), map[string]any{"host": strings.TrimPrefix(server.URL, "http://"), "path": "/other"}, allowLoopback)
	failsWith(t, bad, "403")
}
