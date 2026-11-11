package extension

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tomtly/internal/store"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func shaOf(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// hostileMarket signs a catalog properly and otherwise answers whatever the test wants, as a compromised marketplace could.
type hostileMarket struct {
	pub     string
	priv    ed25519.PrivateKey
	entries []MarketEntry
	ratings string
	answers map[string][]byte
	asked   []string
	posted  []SignedReview
	status  int
	url     string
	bare    bool
}

func newHostileMarket(t *testing.T, change func(*MarketEntry)) *hostileMarket {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := MarketEntry{ID: "tides", Name: "Tides", Kind: "plugin", Version: "1.0.0", Provides: []string{"data"}, SHA256: strings.Repeat("a", 64),
		Package: "/v1/packages/tides-1.0.0.tar.gz", Repository: "https://example.com/a/tides"}
	if change != nil {
		change(&e)
	}
	h := &hostileMarket{pub: base64.StdEncoding.EncodeToString(pub), priv: priv, entries: []MarketEntry{e}, answers: map[string][]byte{}}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	h.url = srv.URL
	return h
}

func (h *hostileMarket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/catalog" {
		payload := catalogPayload(time.Now().UTC(), time.Now().Add(time.Hour), h.entries, nil)
		if h.bare { // what a marketplace from before the two keys sends, under a key that still checks out
			payload, _ = json.Marshal(map[string]any{"generatedAt": time.Now().UTC().Format(time.RFC3339Nano), "entries": h.entries})
		}
		ratings := h.ratings
		if ratings == "" {
			ratings = "null"
		}
		w.Write([]byte(`{"payload":"` + base64.StdEncoding.EncodeToString(payload) + `","signature":"` + base64.StdEncoding.EncodeToString(ed25519.Sign(h.priv, payload)) + `","ratings":` + ratings + `}`))
		return
	}
	h.asked = append(h.asked, r.URL.RequestURI())
	if r.Method == http.MethodPost {
		var s SignedReview
		json.NewDecoder(r.Body).Decode(&s)
		h.posted = append(h.posted, s)
		if h.status != 0 {
			w.WriteHeader(h.status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if data, ok := h.answers[r.URL.Path]; ok {
		w.Write(data)
		return
	}
	http.NotFound(w, r)
}

func hostileManager(t *testing.T, h *hostileMarket) *Manager {
	t.Helper()
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), nil)
	if err := m.SetMarketplace(h.url, h.pub, testApprovalPub); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestImageType(t *testing.T) {
	picture := testPNG(t)
	if kind, err := ImageType(picture); err != nil || kind != "image/png" {
		t.Errorf("png: %q %v", kind, err)
	}
	for name, data := range map[string][]byte{
		"svg":          []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"svg as xml":   []byte(`<?xml version="1.0"?><svg/>`),
		"html":         []byte(`<html><script>alert(1)</script>`),
		"gif":          []byte("GIF89a......"),
		"empty":        {},
		"short png":    picture[:10],
		"bad webp len": []byte("RIFF\xff\xff\xff\xffWEBPVP8 data"),
	} {
		if kind, err := ImageType(data); err == nil {
			t.Errorf("%s was accepted as %s", name, kind)
		}
	}
}

func TestMarketAssetIsCheckedAgainstTheSignedEntry(t *testing.T) {
	picture := testPNG(t)
	h := newHostileMarket(t, func(e *MarketEntry) {
		e.Icon = &MarketAsset{Name: "icon.png", Type: "image/png", SHA256: shaOf(picture)}
		e.Screenshots = []MarketAsset{{Name: "screenshot-1.png", Type: "image/png", SHA256: shaOf(picture)}}
	})
	m := hostileManager(t, h)
	ctx := context.Background()
	h.answers["/v1/assets/tides/1.0.0/icon.png"] = picture
	if data, kind, err := m.MarketAsset(ctx, "tides", "icon.png"); err != nil || kind != "image/png" || !bytes.Equal(data, picture) {
		t.Fatalf("a good image: %q %v", kind, err)
	}

	// The wrong bytes under the right name.
	h.answers["/v1/assets/tides/1.0.0/screenshot-1.png"] = append(bytes.Clone(picture), 0)
	if _, _, err := m.MarketAsset(ctx, "tides", "screenshot-1.png"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("a hash mismatch: %v", err)
	}

	// Names the signed entry doesn't list never reach the marketplace at all.
	asked := len(h.asked)
	for _, name := range []string{"screenshot-2.png", "../../../etc/passwd", "..%2f..%2fv1%2fcatalog", "icon.svg", ""} {
		if _, _, err := m.MarketAsset(ctx, "tides", name); !errors.Is(err, ErrMarketNotListed) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, _, err := m.MarketAsset(ctx, "nobody", "icon.png"); !errors.Is(err, ErrMarketNotListed) {
		t.Errorf("an unlisted id: %v", err)
	}
	if len(h.asked) != asked {
		t.Errorf("asked the marketplace for %v", h.asked[asked:])
	}
}

func TestMarketAssetOverTheLimitIsRefused(t *testing.T) {
	big := append(testPNG(t), make([]byte, MaxMarketIcon)...)
	h := newHostileMarket(t, func(e *MarketEntry) {
		e.Icon = &MarketAsset{Name: "icon.png", Type: "image/png", SHA256: shaOf(big)}
	})
	h.answers["/v1/assets/tides/1.0.0/icon.png"] = big
	if _, _, err := hostileManager(t, h).MarketAsset(context.Background(), "tides", "icon.png"); err == nil {
		t.Error("an oversize image passed, even with a matching hash")
	}
}

func TestMarketAssetOfTheWrongTypeIsRefused(t *testing.T) {
	// It matches its signed hash and claims to be a PNG. Signing it doesn't make it one.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	h := newHostileMarket(t, func(e *MarketEntry) {
		e.Icon = &MarketAsset{Name: "icon.png", Type: "image/png", SHA256: shaOf(svg)}
	})
	h.answers["/v1/assets/tides/1.0.0/icon.png"] = svg
	if _, _, err := hostileManager(t, h).MarketAsset(context.Background(), "tides", "icon.png"); err == nil {
		t.Error("an SVG was passed on as a PNG")
	}
}

func TestSignedEntriesWithBadImagesAreLeftOut(t *testing.T) {
	sum := strings.Repeat("b", 64)
	for name, change := range map[string]func(*MarketEntry){
		"svg type":     func(e *MarketEntry) { e.Icon = &MarketAsset{"icon.png", "image/svg+xml", sum} },
		"svg name":     func(e *MarketEntry) { e.Icon = &MarketAsset{"icon.svg", "image/png", sum} },
		"traversal":    func(e *MarketEntry) { e.Screenshots = []MarketAsset{{"../../secret", "image/png", sum}} },
		"bad hash":     func(e *MarketEntry) { e.Icon = &MarketAsset{"icon.png", "image/png", "nope"} },
		"icon as shot": func(e *MarketEntry) { e.Screenshots = []MarketAsset{{"icon.png", "image/png", sum}} },
		"shot as icon": func(e *MarketEntry) { e.Icon = &MarketAsset{"screenshot-1.png", "image/png", sum} },
		"duplicate": func(e *MarketEntry) {
			e.Screenshots = []MarketAsset{{"screenshot-1.png", "image/png", sum}, {"screenshot-1.png", "image/png", sum}}
		},
		"too many": func(e *MarketEntry) {
			for _, n := range "abcdef" {
				e.Screenshots = append(e.Screenshots, MarketAsset{"screenshot-" + string(n) + ".png", "image/png", sum})
			}
		},
	} {
		h := newHostileMarket(t, change)
		if view := hostileManager(t, h).Marketplace(context.Background()); !view.Verified || len(view.Entries) != 0 {
			t.Errorf("%s: %+v", name, view)
		}
	}
	// An older catalog, with no images at all, stays as it was.
	if view := hostileManager(t, newHostileMarket(t, nil)).Marketplace(context.Background()); !view.Verified || len(view.Entries) != 1 || view.Entries[0].Icon != nil {
		t.Errorf("no images: %+v", view)
	}
}

func TestRatingsFromAHostileMarketplace(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		ratings string
		want    MarketRating
	}{
		"fine":        {`{"tides":{"average":4.256,"count":7}}`, MarketRating{4.26, 7}},
		"too high":    {`{"tides":{"average":99,"count":7}}`, MarketRating{}},
		"too low":     {`{"tides":{"average":-3,"count":7}}`, MarketRating{}},
		"zero":        {`{"tides":{"average":0,"count":7}}`, MarketRating{}},
		"bad count":   {`{"tides":{"average":4,"count":-1}}`, MarketRating{}},
		"not numbers": {`{"tides":{"average":"five","count":"lots"}}`, MarketRating{}},
		"not a map":   {`[1,2,3]`, MarketRating{}},
		"overflow":    {`{"tides":{"average":1e999,"count":1}}`, MarketRating{}},
		"absent":      {``, MarketRating{}},
	} {
		h := newHostileMarket(t, nil)
		h.ratings = c.ratings
		view := hostileManager(t, h).Marketplace(ctx)
		if !view.Verified || len(view.Entries) != 1 {
			t.Errorf("%s: the list suffered for it: %+v", name, view)
			continue
		}
		if view.Entries[0].Rating != c.want {
			t.Errorf("%s: got %+v, want %+v", name, view.Entries[0].Rating, c.want)
		}
	}
}

func TestReviewsFromAHostileMarketplace(t *testing.T) {
	h := newHostileMarket(t, nil)
	reviews := []map[string]any{
		{"id": "a1", "rating": 4, "text": strings.Repeat("é", 5000), "name": strings.Repeat("n", 500), "at": "2026-11-01T10:00:00Z"},
		{"id": "a2", "rating": 9, "text": "too high"},
		{"id": "a3", "rating": 0, "text": "too low"},
		{"id": "a4", "rating": -1, "text": "below"},
		{"id": "<img>", "rating": 3, "text": "bad id"},
		{"id": "a5", "rating": 5, "text": "<script>kept as text</script>"},
	}
	for range 40 {
		reviews = append(reviews, map[string]any{"id": "b", "rating": 2})
	}
	h.answers["/v1/reviews/tides"], _ = json.Marshal(map[string]any{"reviews": reviews, "average": 42.0, "count": 3})
	m := hostileManager(t, h)
	page, err := m.MarketReviews(context.Background(), "tides", 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Reviews) != ReviewPageSize {
		t.Errorf("kept %d reviews", len(page.Reviews))
	}
	if page.MarketRating != (MarketRating{}) {
		t.Errorf("rating: %+v", page.MarketRating)
	}
	first := page.Reviews[0]
	if first.ID != "a1" || len([]rune(first.Text)) != MaxReviewText || len([]rune(first.Name)) != MaxReviewName {
		t.Errorf("not shortened: %q %d %d", first.ID, len([]rune(first.Text)), len([]rune(first.Name)))
	}
	if page.Reviews[1].ID != "a5" {
		t.Errorf("out-of-range ones should go: %+v", page.Reviews[1])
	}
	if got := h.asked[len(h.asked)-1]; got != "/v1/reviews/tides?limit=20&offset=40" {
		t.Errorf("asked %s", got)
	}

	h.answers["/v1/reviews/tides"] = []byte("not json")
	if _, err := m.MarketReviews(context.Background(), "tides", 0); err == nil {
		t.Error("garbage should be an error")
	}
	if _, err := m.MarketReviews(context.Background(), "nobody", 0); !errors.Is(err, ErrMarketNotListed) {
		t.Errorf("unlisted: %v", err)
	}
}

func TestMarketPostReviewSignsWithOneKeyAndSendsNothingElse(t *testing.T) {
	h := newHostileMarket(t, nil)
	m := hostileManager(t, h)
	ctx := context.Background()
	for _, text := range []string{"  first  ", "second"} {
		if err := m.MarketPostReview(ctx, "tides", 4, text, "  Ann "); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.posted) != 2 || h.posted[0].Key != h.posted[1].Key {
		t.Fatalf("one install should keep one key: %+v", h.posted)
	}
	s := h.posted[0]
	key, _ := base64.StdEncoding.DecodeString(s.Key)
	payload, _ := base64.StdEncoding.DecodeString(s.Payload)
	signature, _ := base64.StdEncoding.DecodeString(s.Signature)
	if !ed25519.Verify(key, payload, signature) {
		t.Error("the signature doesn't verify")
	}
	var got map[string]any
	json.Unmarshal(payload, &got)
	if len(got) != 5 || got["listing"] != "tides" || got["rating"] != 4.0 || got["text"] != "first" || got["name"] != "Ann" || got["timestamp"] == nil {
		t.Errorf("payload: %v", got)
	}

	for _, c := range []struct {
		status, want int
	}{{http.StatusUnauthorized, 400}, {http.StatusTooManyRequests, 429}, {http.StatusConflict, 409}, {http.StatusBadRequest, 400}} {
		h.status = c.status
		var refused *ReviewRefused
		if err := m.MarketPostReview(ctx, "tides", 4, "", ""); !errors.As(err, &refused) || refused.Status != c.want {
			t.Errorf("marketplace %d: %v", c.status, err)
		}
	}
	h.status = http.StatusInternalServerError
	if err := m.MarketPostReview(ctx, "tides", 4, "", ""); !errors.Is(err, errUnreachable) {
		t.Errorf("a broken marketplace: %v", err)
	}
	h.status = 0

	n := len(h.posted)
	for name, post := range map[string]func() error{
		"rating":   func() error { return m.MarketPostReview(ctx, "tides", 6, "", "") },
		"text":     func() error { return m.MarketPostReview(ctx, "tides", 4, strings.Repeat("x", 2001), "") },
		"name":     func() error { return m.MarketPostReview(ctx, "tides", 4, "", strings.Repeat("x", 61)) },
		"control":  func() error { return m.MarketPostReview(ctx, "tides", 4, "a\x00b", "") },
		"unlisted": func() error { return m.MarketPostReview(ctx, "nobody", 4, "", "") },
	} {
		if post() == nil {
			t.Errorf("%s was sent", name)
		}
	}
	if len(h.posted) != n {
		t.Error("something invalid reached the marketplace")
	}
}

func TestMarketUnreachableForReviews(t *testing.T) {
	m := newManager(t.TempDir(), t.TempDir(), store.NewMemory(), nil)
	ctx := context.Background()
	if _, err := m.MarketReviews(ctx, "tides", 0); !errors.Is(err, ErrMarketNotListed) {
		t.Errorf("not set up: %v", err)
	}
	if err := m.MarketPostReview(ctx, "tides", 4, "", ""); !errors.Is(err, ErrMarketNotListed) {
		t.Errorf("not set up: %v", err)
	}
}
