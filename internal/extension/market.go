package extension

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"

	"slices"

	"strings"
	"sync"
	"time"

	"tomtly/internal/store"
)

const (
	maxCatalog   = 4 << 20
	maxMarketPkg = 16 << 20
	catalogTime  = 15 * time.Second
	packageTime  = 60 * time.Second
)

var (
	errUnreachable = errors.New("the marketplace can't be reached right now")
	errUnverified  = errors.New("the marketplace can't be verified, so nothing from it is offered")
)

// MarketEntry is one extension in the signed catalog.
type MarketEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Homepage    string `json:"homepage"`
	// License is the SPDX id of the open license the package is under. Older catalogs have none.
	License    string   `json:"license,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Provides   []string `json:"provides"`
	Access     []string `json:"access"`
	TestedOn   string   `json:"testedOn"`
	ApprovedOn string   `json:"approvedOn"`
	SHA256     string   `json:"sha256"`
	Size       int      `json:"size"`
	Package    string   `json:"package"`
	// Repository is the author's own link. Nothing checks it: what was verified is the package with this SHA256.
	Repository string `json:"repository,omitempty"`
	// Migrations lets the list say what an update needs before anyone downloads it. The package's own files decide what happens.
	Migrations []Migration `json:"migrations,omitempty"`
	// The images this listing shows, pinned by the signature. Older catalogs have none.
	Icon        *MarketAsset  `json:"icon,omitempty"`
	Screenshots []MarketAsset `json:"screenshots,omitempty"`
}

// MarketListing is an entry as the page sees it, next to what is installed.
type MarketListing struct {
	MarketEntry
	// Installed means this listing's own marketplace copy is installed. DifferentCopy means the id is taken by an upload,
	// a GitHub repo or a folder, which only claims to be this listing.
	Installed        bool   `json:"installed"`
	DifferentCopy    bool   `json:"differentCopy,omitempty"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	InstalledSource  string `json:"installedSource,omitempty"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	// UpgradeSteps is the migrations between the installed version and this one.
	UpgradeSteps []Migration `json:"upgradeSteps,omitempty"`
	// Suspended is why new installs are stopped for now. Empty if they aren't.
	Suspended string `json:"suspended,omitempty"`
	// Rating isn't signed: it comes beside the catalog, and is only as good as the marketplace says.
	Rating MarketRating `json:"rating"`
	// Downloads isn't signed either, and counts every download, reinstalls included.
	Downloads int64 `json:"downloads"`
}

// MarketView is what the page gets. Verified is false when the marketplace answered but its list can't be trusted.
type MarketView struct {
	Configured bool            `json:"configured"`
	Reachable  bool            `json:"reachable"`
	Verified   bool            `json:"verified"`
	Error      string          `json:"error,omitempty"`
	Entries    []MarketListing `json:"entries"`
	// Site is the marketplace's address; its website, where people browse and choose Get, is at its root.
	Site string `json:"site,omitempty"`
}

type marketplace struct {
	url            string
	keys, approval []ed25519.PublicKey // the catalog is signed by keys, each entry in it by approval
	problem        string              // why nothing can be trusted however it is signed
	client         *http.Client
	store          store.Store

	mu           sync.Mutex
	loaded       bool
	newest       time.Time // the latest catalog seen, kept across restarts; an older one is a replay
	newestSigned time.Time
	revoked      []MarketRevocation
	entries      []MarketEntry
	ratings      map[string]MarketRating
	downloads    map[string]int64
	cachedAt     time.Time // when entries and ratings were fetched
}

// checkMarketAddress refuses plain HTTP across the internet. The signature protects the list, but not who sees what a home
// asks for. A local address is fine: it never leaves the network.
func checkMarketAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("MARKETPLACE_URL must be an address like https://market.example.com")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !strings.Contains(host, ".") && ip == nil || host == "host.docker.internal" || ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		return nil
	}
	return errors.New("MARKETPLACE_URL must use https unless it is on your own network")
}

// ParseMarketKeys reads a comma-separated list of base64 Ed25519 public keys. More than one is how a key is rotated.
func ParseMarketKeys(list string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, item := range strings.Split(list, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(item)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%q is not a base64 Ed25519 public key", item)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// SignedByAny says whether one of the keys made this signature.
func SignedByAny(keys []ed25519.PublicKey, message, signature []byte) bool {
	return len(signature) == ed25519.SignatureSize && slices.ContainsFunc(keys, func(k ed25519.PublicKey) bool { return ed25519.Verify(k, message, signature) })
}

// SetMarketplace points the manager at a marketplace. publicKeys sign its list, approvalKeys each entry in it, and both are
// lists so a key can be rotated. Blank address and keys leave it not set up, which the page says. With no approval keys, or
// the same key in both lists, it stays set up and refuses everything, and the page says what to set.
func (m *Manager) SetMarketplace(address, publicKeys, approvalKeys string) error {
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	if address == "" && strings.TrimSpace(publicKeys) == "" {
		return nil
	}
	keys, err := ParseMarketKeys(publicKeys)
	if address == "" || err != nil || len(keys) == 0 {
		return errors.New("MARKETPLACE_URL and MARKETPLACE_PUBLIC_KEY (base64 Ed25519 public keys, separated by commas) must both be set")
	}
	approval, err := ParseMarketKeys(approvalKeys)
	if err != nil {
		return fmt.Errorf("MARKETPLACE_APPROVAL_PUBLIC_KEYS: %w", err)
	}
	if err := checkMarketAddress(address); err != nil {
		return err
	}
	mp := &marketplace{url: address, keys: keys, approval: approval, store: m.store, client: &http.Client{
		// A redirect could send the download somewhere the signed catalog never named.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	switch {
	case len(approval) == 0:
		mp.problem = "MARKETPLACE_APPROVAL_PUBLIC_KEYS isn't set, so nothing the marketplace lists can be trusted as approved. Set it to the marketplace's approval public key and restart Tomtly"
	case slices.ContainsFunc(approval, func(k ed25519.PublicKey) bool {
		return slices.ContainsFunc(keys, func(other ed25519.PublicKey) bool { return k.Equal(other) })
	}):
		mp.problem = "the same key is in MARKETPLACE_PUBLIC_KEY and MARKETPLACE_APPROVAL_PUBLIC_KEYS, which would let the marketplace's web service approve anything. They must be different keys"
	}
	m.market = mp
	if m.ctx != nil {
		go m.watchMarket(m.ctx, marketPollEvery)
	}
	return nil
}

func (mp *marketplace) get(ctx context.Context, path string, limit int64, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mp.url+path, nil)
	if err != nil {
		return nil, errUnreachable
	}
	resp, err := mp.client.Do(req)
	if err != nil {
		return nil, errUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w (it answered %s)", errUnreachable, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errUnreachable
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: its answer is too big", errUnverified)
	}
	return data, nil
}

// catalog fetches the list and trusts nothing in it until the web key's signature over the exact bytes checks out, the list
// is fresh, and each entry has the approval key's signature over its own exact bytes.
func (mp *marketplace) catalog(ctx context.Context) ([]MarketEntry, error) {
	if mp.problem != "" {
		return nil, fmt.Errorf("%w: %s", errUnverified, mp.problem)
	}
	body, err := mp.get(ctx, "/v1/catalog", maxCatalog, catalogTime)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Payload   string          `json:"payload"`
		Signature string          `json:"signature"`
		Ratings   json.RawMessage `json:"ratings"`
		Downloads json.RawMessage `json:"downloads"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, errUnverified
	}
	payload, err1 := base64.StdEncoding.DecodeString(envelope.Payload)
	signature, err2 := base64.StdEncoding.DecodeString(envelope.Signature)
	if err1 != nil || err2 != nil || !SignedByAny(mp.keys, payload, signature) {
		return nil, errUnverified
	}
	var list struct {
		GeneratedAt time.Time `json:"generatedAt"`
		SignedAt    time.Time `json:"signedAt"`
		ExpiresAt   time.Time `json:"expiresAt"`
		Entries     []struct {
			Entry     string `json:"entry"`
			Signature string `json:"signature"`
		} `json:"entries"`
		Revoked []MarketRevocation `json:"revoked"`
	}
	if json.Unmarshal(payload, &list) != nil || list.GeneratedAt.IsZero() || list.SignedAt.IsZero() || list.ExpiresAt.IsZero() {
		return nil, errUnverified
	}
	if time.Now().After(list.ExpiresAt) {
		return nil, fmt.Errorf("%w: its list expired on %s. It may be stopped, someone may be holding back newer lists, or this computer's clock may be wrong", errUnverified, list.ExpiresAt.UTC().Format(time.DateOnly))
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if err := mp.load(ctx); err != nil {
		return nil, err
	}
	if list.GeneratedAt.Before(mp.newest) || list.SignedAt.Before(mp.newestSigned) {
		return nil, fmt.Errorf("%w: its list is older than one already seen", errUnverified)
	}
	if err := mp.remember(ctx, list.GeneratedAt, list.SignedAt, list.Revoked); err != nil {
		return nil, err
	}
	entries := make([]MarketEntry, 0, len(list.Entries))
	for _, signed := range list.Entries {
		raw, err1 := base64.StdEncoding.DecodeString(signed.Entry)
		signature, err2 := base64.StdEncoding.DecodeString(signed.Signature)
		var e MarketEntry
		// An entry that doesn't pass is left out, as if the marketplace hadn't offered it.
		if err1 != nil || err2 != nil || !SignedByAny(mp.approval, raw, signature) || json.Unmarshal(raw, &e) != nil || e.valid() != nil {
			slog.Error("the marketplace listed an entry that isn't approved, so it was left out", "entry", clipText(e.ID, 64))
			continue
		}
		entries = append(entries, e)
	}
	mp.entries, mp.cachedAt = entries, time.Now()
	mp.ratings = readRatings(envelope.Ratings)
	mp.downloads = readDownloads(envelope.Downloads)
	return entries, nil
}

// valid keeps a signed entry from naming a package anywhere but its own place on the marketplace.
func (e MarketEntry) valid() error {
	match := marketSource.FindStringSubmatch(e.source())
	if match == nil {
		return errors.New("the entry has a bad id, version or hash")
	}
	if e.Package != "/v1/packages/"+e.ID+"-"+e.Version+".tar.gz" {
		return errors.New("the entry names a package somewhere else")
	}
	if e.Size < 0 || e.Size > maxMarketPkg {
		return errors.New("the package is too big")
	}
	if e.Repository != "" && !strings.HasPrefix(e.Repository, "https://") && !strings.HasPrefix(e.Repository, "http://") {
		return errors.New("the entry's link isn't a web address")
	}
	if err := e.assetsValid(); err != nil {
		return err
	}
	return checkListingWords(e.Categories, e.Tags, e.License)
}

func (e MarketEntry) source() string { return "market:" + e.ID + "@" + e.Version + "#" + e.SHA256 }

// Marketplace lists what the marketplace offers, with what is installed. It never fails: the page shows why it is empty.
func (m *Manager) Marketplace(ctx context.Context) MarketView {
	view := MarketView{Entries: []MarketListing{}}
	if m.market == nil {
		return view
	}
	view.Configured, view.Site = true, m.market.url
	entries, err := m.market.catalog(ctx)
	switch {
	case errors.Is(err, errUnreachable):
		view.Error = err.Error()
		return view
	case err != nil:
		view.Reachable, view.Error = true, err.Error()
		return view
	}
	view.Reachable, view.Verified = true, true
	installed := m.installedByID()
	for _, e := range entries {
		view.Entries = append(view.Entries, m.listingOf(e, installed))
	}
	return view
}

func (m *Manager) installedByID() map[string]Status {
	installed := map[string]Status{}
	for _, s := range m.Statuses() {
		if s.ID != "" {
			installed[s.ID] = s
		}
	}
	return installed
}

// listingOf is an entry next to what is installed with its id.
func (m *Manager) listingOf(e MarketEntry, installed map[string]Status) MarketListing {
	l := MarketListing{MarketEntry: e, Rating: m.market.rating(e.ID), Suspended: m.market.suspended(e.ID, e.Version), Downloads: m.market.downloadsOf(e.ID)}
	if have, ok := installed[e.ID]; ok {
		if match := marketSource.FindStringSubmatch(have.Source); match == nil || match[1] != e.ID {
			l.DifferentCopy = true
		} else {
			l.Installed, l.InstalledVersion, l.InstalledSource = true, have.Version, have.Source
			l.UpdateAvailable = l.Suspended == "" && newerVersion(e.Version, have.Version)
			if l.UpdateAvailable {
				l.UpgradeSteps = migrationsBetween(e.Migrations, have.Version, e.Version)
			}
		}
	}
	return l
}

// MarketInspect downloads an entry, checks it is what the signed list says, and stores it. Like Inspect, it starts
// nothing: the owner still approves the access that the code really asks for.
func (m *Manager) MarketInspect(ctx context.Context, id string) (Status, error) {
	if m.market == nil {
		return Status{}, errors.New("the marketplace isn't set up")
	}
	entries, err := m.market.catalog(ctx)
	if err != nil {
		return Status{}, err
	}
	i := slices.IndexFunc(entries, func(e MarketEntry) bool { return e.ID == id })
	if i < 0 {
		return Status{}, errors.New("the marketplace no longer offers that")
	}
	e := entries[i]
	if reason := m.market.suspended(e.ID, e.Version); reason != "" {
		return Status{}, suspendedError(reason)
	}
	data, err := m.market.get(ctx, e.Package, maxMarketPkg, packageTime)
	if err != nil {
		return Status{}, err
	}
	if sha256Hex(data) != e.SHA256 {
		return Status{}, errors.New("the download isn't what the marketplace listed (its checksum is different), so it was thrown away")
	}
	source := e.source()
	key, _ := packageKey(source)
	stored, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := m.store.SavePackage(stored, key, data); err != nil {
		return Status{}, fmt.Errorf("could not save the download: %w", err)
	}
	status, declared, err := m.inspect(ctx, source)
	if err == nil {
		got := slices.Sorted(slices.Values(declared))
		if !slices.Equal(got, slices.Sorted(slices.Values(e.Access))) {
			err = errors.New("the access the marketplace listed isn't what the code really asks for, so it was refused")
		}
	}
	if err != nil {
		m.dropUnused(source)
		return Status{}, err
	}
	return status, nil
}

// readDownloads keeps what can be read of the counts beside the signature. A count is only as good as the marketplace says.
func readDownloads(raw json.RawMessage) map[string]int64 {
	downloads := map[string]int64{}
	if len(raw) == 0 || json.Unmarshal(raw, &downloads) != nil {
		return nil
	}
	return downloads
}

func (mp *marketplace) downloadsOf(id string) int64 {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return max(mp.downloads[id], 0)
}
