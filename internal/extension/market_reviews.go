package extension

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxReviewText  = 2000
	MaxReviewName  = 60
	ReviewPageSize = 20
	maxReviewsPage = 1 << 20
	reviewTime     = 10 * time.Second
)

var reviewID = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// MarketRating is a listing's average and how many reviews it is from. Nothing vouches for it: it isn't signed.
type MarketRating struct {
	Average float64 `json:"average"`
	Count   int     `json:"count"`
}

// clean turns a rating no marketplace should send into no rating at all.
func (r MarketRating) clean() MarketRating {
	if r.Count <= 0 || r.Average < 1 || r.Average > 5 {
		return MarketRating{}
	}
	r.Average = math.Round(r.Average*100) / 100
	return r
}

// readRatings keeps what can be read of the part beside the signature. The catalog itself doesn't depend on it.
func readRatings(raw json.RawMessage) map[string]MarketRating {
	ratings := map[string]MarketRating{}
	if len(raw) == 0 || json.Unmarshal(raw, &ratings) != nil {
		return nil
	}
	return ratings
}

func (mp *marketplace) rating(id string) MarketRating {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return mp.ratings[id].clean()
}

// MarketReview is one person's review. All of it is whatever a stranger typed, passed on by a service that isn't trusted.
type MarketReview struct {
	ID     string    `json:"id"`
	Rating int       `json:"rating"`
	Text   string    `json:"text"`
	Name   string    `json:"name"`
	At     time.Time `json:"at"`
}

// MarketReviews is one page of a listing's reviews and its rating.
type MarketReviews struct {
	Reviews []MarketReview `json:"reviews"`
	MarketRating
}

func capRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

// MarketReviews fetches a page of reviews through the home, and drops or shortens anything out of bounds.
func (m *Manager) MarketReviews(ctx context.Context, id string, offset int) (MarketReviews, error) {
	e, err := m.marketEntry(ctx, id)
	if err != nil {
		return MarketReviews{}, err
	}
	data, err := m.market.get(ctx, "/v1/reviews/"+e.ID+"?limit="+strconv.Itoa(ReviewPageSize)+"&offset="+strconv.Itoa(offset), maxReviewsPage, reviewTime)
	if err != nil {
		return MarketReviews{}, err
	}
	var page MarketReviews
	if json.Unmarshal(data, &page) != nil {
		return MarketReviews{}, errors.New("the marketplace's reviews can't be read")
	}
	out := MarketReviews{Reviews: []MarketReview{}, MarketRating: page.MarketRating.clean()}
	for _, r := range page.Reviews {
		if len(out.Reviews) == ReviewPageSize {
			break
		}
		if r.Rating < 1 || r.Rating > 5 || !reviewID.MatchString(r.ID) {
			continue
		}
		r.Text, r.Name = capRunes(r.Text, MaxReviewText), capRunes(r.Name, MaxReviewName)
		out.Reviews = append(out.Reviews, r)
	}
	return out, nil
}

// CheckReview is the one rule for what a review may hold, here and on the marketplace.
func CheckReview(rating int, text, name string) error {
	switch {
	case rating < 1 || rating > 5:
		return errors.New("the rating must be from 1 to 5")
	case utf8.RuneCountInString(text) > MaxReviewText:
		return fmt.Errorf("the review must be at most %d characters", MaxReviewText)
	case utf8.RuneCountInString(name) > MaxReviewName:
		return fmt.Errorf("the name must be at most %d characters", MaxReviewName)
	case !utf8.ValidString(text) || !utf8.ValidString(name):
		return errors.New("the review isn't valid text")
	case strings.ContainsFunc(name, unicode.IsControl):
		return errors.New("the name can't hold line breaks or control characters")
	case strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }):
		return errors.New("the review can't hold control characters")
	}
	return nil
}

// SignedReview is what a home sends: the key, the exact bytes it signed, and the signature. Checking those bytes, as the
// catalog's signature does, means neither side has to rebuild them the same way.
type SignedReview struct {
	Key       string `json:"key"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// ReviewPayload is what a review's signature covers.
type ReviewPayload struct {
	Listing   string `json:"listing"`
	Rating    int    `json:"rating"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	Timestamp int64  `json:"timestamp"`
}

// ReviewRefused is a post the person can do something about. Status goes to the page. The message is ours, never the
// marketplace's, and it is never 401, which the page would take for a lost login.
type ReviewRefused struct {
	Status  int
	Message string
}

func (e *ReviewRefused) Error() string { return e.Message }

// installKey is made the first time this home posts a review, so a home that never does has no identity at all.
func (m *Manager) installKey(ctx context.Context) (ed25519.PrivateKey, error) {
	candidate := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(candidate); err != nil {
		return nil, err
	}
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	seed, err := m.store.InstallKey(stored, candidate)
	if err != nil {
		return nil, fmt.Errorf("could not read this install's key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("this install's saved key is damaged")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// MarketPostReview signs a review and sends it. These fields and the key are all a home ever sends.
func (m *Manager) MarketPostReview(ctx context.Context, id string, rating int, text, name string) error {
	text, name = strings.TrimSpace(text), strings.TrimSpace(name)
	if err := CheckReview(rating, text, name); err != nil {
		return &ReviewRefused{http.StatusBadRequest, err.Error()}
	}
	e, err := m.marketEntry(ctx, id)
	if err != nil {
		return err
	}
	key, err := m.installKey(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(ReviewPayload{e.ID, rating, text, name, time.Now().Unix()})
	if err != nil {
		return err
	}
	body, err := json.Marshal(SignedReview{
		Key:       base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)),
	})
	if err != nil {
		return err
	}
	posting, cancel := context.WithTimeout(ctx, reviewTime)
	defer cancel()
	req, err := http.NewRequestWithContext(posting, http.MethodPost, m.market.url+"/v1/reviews/"+e.ID, bytes.NewReader(body))
	if err != nil {
		return errUnreachable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.market.client.Do(req)
	if err != nil {
		return errUnreachable
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return ErrMarketNotListed
	case http.StatusUnauthorized:
		return &ReviewRefused{http.StatusBadRequest, "The marketplace didn't accept this review's signature. Check that this computer's clock is right."}
	case http.StatusConflict:
		return &ReviewRefused{http.StatusConflict, "The marketplace already has a newer review from this home."}
	case http.StatusTooManyRequests:
		return &ReviewRefused{http.StatusTooManyRequests, "Too many reviews from here. Try again later."}
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return &ReviewRefused{http.StatusBadRequest, "The marketplace didn't accept this review."}
	}
	return errUnreachable
}
