package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"tomtly/internal/extension"
)

func TestTheListenerHasLimitsThatStillFitTheBiggestUpload(t *testing.T) {
	srv := httpServer(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 || srv.IdleTimeout <= 0 || srv.ReadTimeout <= 0 {
		t.Fatalf("a slow client could hold a connection forever: %+v", srv)
	}
	const slowLink = 500_000 // bits a second
	upload := time.Duration(extension.MaxUpload*8/slowLink) * time.Second
	if srv.ReadTimeout < upload {
		t.Errorf("16 MiB takes %v on a slow link, and the limit is %v", upload, srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Error("inspecting and downloading take minutes, and a write limit would cut them off")
	}
}

func TestRoomOrderIsTheFiles(t *testing.T) {
	got := objectKeys([]byte(`{"timezone":"UTC","rooms":{"Living room":["a"],"Bedroom":[],"Attic":["b","c"]}}`), "rooms")
	if strings.Join(got, "|") != "Living room|Bedroom|Attic" {
		t.Errorf("got %q", got)
	}
	for _, data := range []string{`{}`, `{"rooms":[]}`, `not json`, `{"rooms":null}`} {
		if got := objectKeys([]byte(data), "rooms"); len(got) != 0 {
			t.Errorf("%s: %q", data, got)
		}
	}
}
