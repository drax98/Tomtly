package extension

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"net/http"
	"regexp"
	"slices"
	"time"
)

const (
	MaxMarketIcon        = 256 << 10
	MaxMarketScreenshot  = 2 << 20
	MaxMarketScreenshots = 5
	assetTime            = 20 * time.Second
	// catalogFresh is how long one fetched catalog answers for its images and reviews. A page with many icons would
	// otherwise fetch and verify the whole list once for each.
	catalogFresh = time.Minute
)

var (
	// ErrMarketNotListed means the verified catalog has no such listing or image.
	ErrMarketNotListed = errors.New("the marketplace no longer offers that")

	marketIconName       = regexp.MustCompile(`^icon\.(png|jpe?g|webp)$`)
	marketScreenshotName = regexp.MustCompile(`^screenshot-[A-Za-z0-9_-]{1,40}\.(png|jpe?g|webp)$`)
	sha256Format         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// MarketAsset is one image of a listing, named in the signed catalog so its bytes are pinned.
type MarketAsset struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
}

// MarketAssetLimit says whether a file name can be a listing's image, and how big that image may be.
func MarketAssetLimit(name string) (limit int, ok bool) {
	switch {
	case marketIconName.MatchString(name):
		return MaxMarketIcon, true
	case marketScreenshotName.MatchString(name):
		return MaxMarketScreenshot, true
	}
	return 0, false
}

// ImageType identifies an image by its content. Anything but PNG, JPEG and WebP is refused, SVG above all, since it can
// carry script. The standard library has no WebP reader, so that one is checked as far as its container.
func ImageType(data []byte) (string, error) {
	var err error
	kind := http.DetectContentType(data)
	switch kind {
	case "image/png":
		_, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		_, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		if uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
			err = errors.New("its length is wrong")
		}
	default:
		return "", errors.New("it isn't a PNG, JPEG or WebP image")
	}
	if err != nil {
		return "", fmt.Errorf("it isn't a readable %s: %w", kind, err)
	}
	return kind, nil
}

func (e MarketEntry) assetsValid() error {
	if len(e.Screenshots) > MaxMarketScreenshots {
		return errors.New("the entry has too many screenshots")
	}
	all := e.Screenshots
	if e.Icon != nil {
		if !marketIconName.MatchString(e.Icon.Name) {
			return errors.New("the entry has a bad icon name")
		}
		all = append(slices.Clone(all), *e.Icon)
	}
	seen := map[string]bool{}
	for _, a := range all {
		_, named := MarketAssetLimit(a.Name)
		if !named || seen[a.Name] || !sha256Format.MatchString(a.SHA256) || (a.Type != "image/png" && a.Type != "image/jpeg" && a.Type != "image/webp") {
			return errors.New("the entry has a bad image")
		}
		seen[a.Name] = true
	}
	for _, a := range e.Screenshots {
		if !marketScreenshotName.MatchString(a.Name) {
			return errors.New("the entry has a bad screenshot name")
		}
	}
	return nil
}

// recent is the verified catalog, fetched only if the last one is old.
func (mp *marketplace) recent(ctx context.Context) ([]MarketEntry, error) {
	mp.mu.Lock()
	entries, at := mp.entries, mp.cachedAt
	mp.mu.Unlock()
	if !at.IsZero() && time.Since(at) < catalogFresh {
		return entries, nil
	}
	return mp.catalog(ctx)
}

// marketEntry finds a listing in the verified catalog. A blank marketplace offers nothing.
func (m *Manager) marketEntry(ctx context.Context, id string) (MarketEntry, error) {
	if m.market == nil {
		return MarketEntry{}, ErrMarketNotListed
	}
	entries, err := m.market.recent(ctx)
	if err != nil {
		return MarketEntry{}, err
	}
	i := slices.IndexFunc(entries, func(e MarketEntry) bool { return e.ID == id })
	if i < 0 {
		return MarketEntry{}, ErrMarketNotListed
	}
	return entries[i], nil
}

// MarketAsset fetches one image the signed catalog names, for the page, which never talks to the marketplace itself: that
// would hand it the home's address and a way to track who looks. The address is built only from the signed entry.
func (m *Manager) MarketAsset(ctx context.Context, id, name string) ([]byte, string, error) {
	e, err := m.marketEntry(ctx, id)
	if err != nil {
		return nil, "", err
	}
	all := e.Screenshots
	if e.Icon != nil {
		all = append(slices.Clone(all), *e.Icon)
	}
	i := slices.IndexFunc(all, func(a MarketAsset) bool { return a.Name == name })
	if i < 0 {
		return nil, "", ErrMarketNotListed
	}
	asset := all[i]
	limit, _ := MarketAssetLimit(asset.Name)
	data, err := m.market.get(ctx, "/v1/assets/"+e.ID+"/"+e.Version+"/"+asset.Name, int64(limit), assetTime)
	if err != nil {
		return nil, "", err
	}
	if sha256Hex(data) != asset.SHA256 {
		return nil, "", errors.New("the image isn't what the marketplace listed (its checksum is different)")
	}
	if kind, err := ImageType(data); err != nil || kind != asset.Type {
		return nil, "", errors.New("the image isn't the type the marketplace listed")
	}
	return data, asset.Type, nil
}
