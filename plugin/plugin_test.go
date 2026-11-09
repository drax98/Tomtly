package plugin

import (
	"errors"
	"strings"
	"testing"
)

func TestKeepsLastGoodDataThroughAFailure(t *testing.T) {
	h := NewHub()
	if err := h.Claim("weather", "extension"); err != nil {
		t.Fatal(err)
	}
	h.Publish("weather", []byte(`{"temperature":5}`), nil)
	h.Publish("weather", nil, errors.New("down"))
	data, _, ok := h.Get("weather")
	if !ok || string(data) != `{"temperature":5}` {
		t.Fatalf("data = %s, %v", data, ok)
	}
	if got := h.Statuses()[0].Error; got != "down" {
		t.Fatalf("error = %q", got)
	}
	h.Publish("weather", []byte(`{"temperature":6}`), nil)
	if got := h.Statuses()[0].Error; got != "" {
		t.Fatalf("a success should clear the error, got %q", got)
	}
}

func TestAnIDCanOnlyBeClaimedOnce(t *testing.T) {
	h := NewHub()
	if err := h.Claim("a", "extension"); err != nil {
		t.Fatal(err)
	}
	if err := h.Claim("a", "extension"); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("got %v", err)
	}
	h.Remove("a")
	if err := h.Claim("a", "extension"); err != nil {
		t.Fatal(err)
	}
}

func TestActOnlyRunsDeclaredActions(t *testing.T) {
	h := NewHub()
	h.Claim("a", "extension")
	var got string
	h.SetActions("a", []string{"go"}, func(name string, args []byte) error { got = name + string(args); return nil })
	if err := h.Act("a", "go", []byte("1")); err != nil || got != "go1" {
		t.Fatalf("%v, %q", err, got)
	}
	if err := h.Act("a", "other", nil); err == nil {
		t.Fatal("an undeclared action ran")
	}
}
