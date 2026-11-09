// Command virtual-lamp is the smallest possible external adapter: one fake lamp, standard library only.
// Copy it, or write the same two endpoints in any language. See docs/devices.md.
//
//	go run ./examples/adapters/virtual-lamp
//
// Then add { "type": "http", "name": "demo", "url": "http://localhost:9000" } to config.json.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

func main() {
	var mu sync.Mutex
	on := false

	http.HandleFunc("GET /devices", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode([]map[string]any{{
			"id": "lamp", "name": "Virtual lamp", "kind": "light", "online": true,
			"dps": []map[string]any{{"dp": "1", "code": "switch", "type": "Boolean", "values": map[string]any{}, "writable": true, "value": on}},
		}})
		if err != nil {
			log.Print(err)
		}
	})

	http.HandleFunc("POST /devices/{id}/dps/{dp}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value bool `json:"value"`
		}
		if r.PathValue("id") != "lamp" || r.PathValue("dp") != "1" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		on = body.Value
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})

	log.Print("virtual lamp on :9000")
	log.Fatal(http.ListenAndServe(":9000", nil))
}
