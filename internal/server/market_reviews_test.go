package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"home-control/internal/extension"
)

// marketReview answers the marketplace's image and review calls with what the test sets.
type marketReview struct {
	fakeExtensions
	err error
}

func (m marketReview) MarketAsset(_ context.Context, id, name string) ([]byte, string, error) {
	return []byte("image bytes"), "image/webp", m.err
}
func (m marketReview) MarketReviews(context.Context, string, int) (extension.MarketReviews, error) {
	return extension.MarketReviews{Reviews: []extension.MarketReview{}, MarketRating: extension.MarketRating{Average: 4, Count: 1}}, m.err
}
func (m marketReview) MarketPostReview(context.Context, string, int, string, string) error {
	return m.err
}

func TestMarketplaceAssetsAndReviewsThroughTheHome(t *testing.T) {
	g := newRig(t)
	root := g.admin()
	g.a.extensions = marketReview{}
	g.h = g.a.handler()

	w := g.do(opts{cookie: root}, "GET", "/api/marketplace/tides/assets/icon.png", "")
	g.want(w, 200)
	h := w.Header()
	if h.Get("Content-Type") != "image/webp" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("headers: %v", h)
	}
	g.want(g.do(opts{cookie: root}, "GET", "/api/marketplace/tides/reviews", ""), 200)
	g.want(g.do(opts{cookie: root}, "GET", "/api/marketplace/tides/reviews?offset=-1", ""), 400)
	g.want(g.do(opts{cookie: root}, "GET", "/api/marketplace/tides/reviews?offset=x", ""), 400)
	g.want(g.do(opts{cookie: root}, "POST", "/api/marketplace/tides/reviews", `{"rating":5,"text":"secret words","name":"Ann"}`), 200)
	if !g.audited("marketplace.review", "tides rated 5") {
		t.Errorf("not audited: %+v", g.audit())
	}
	if g.audited("marketplace.review", "secret words") {
		t.Error("the audit log needs the fact, not the text")
	}

	// A person who may look can't post.
	g.user("v", "viewer")
	viewer := g.login("v")
	g.want(g.do(opts{cookie: viewer}, "GET", "/api/marketplace/tides/reviews", ""), 200)
	g.want(g.do(opts{cookie: viewer}, "POST", "/api/marketplace/tides/reviews", `{"rating":5}`), 403)

	n := len(g.audit())
	for err, code := range map[error]int{
		errors.New("the marketplace can't be reached"):             502,
		extension.ErrMarketNotListed:                               404,
		&extension.ReviewRefused{Status: 429, Message: "too many"}: 429,
	} {
		g.a.extensions = marketReview{err: err}
		g.h = g.a.handler()
		g.want(g.do(opts{cookie: root}, "POST", "/api/marketplace/tides/reviews", `{"rating":5}`), code)
	}
	g.a.extensions = marketReview{err: errors.New("down")}
	g.h = g.a.handler()
	g.want(g.do(opts{cookie: root}, "GET", "/api/marketplace/tides/assets/icon.png", ""), 502)
	g.want(g.do(opts{cookie: root}, "GET", "/api/marketplace/tides/reviews", ""), 502)
	if len(g.audit()) != n {
		t.Error("a refused post was audited as if it happened")
	}
}
