package extension

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// market:<id>@<version>#<sha256 of the package>. The hash is enough to find the stored copy without the marketplace.
var marketSource = regexp.MustCompile(`^market:([a-z0-9][a-z0-9-]{0,63})@([0-9A-Za-z][0-9A-Za-z.+-]{0,63})#([0-9a-f]{64})$`)

// packageKey is where a source's archive is kept in the store, for the sources that are kept there.
func packageKey(source string) (string, bool) {
	if key, ok := strings.CutPrefix(source, "upload:"); ok {
		return key, true
	}
	if match := marketSource.FindStringSubmatch(source); match != nil {
		return "market-" + match[3], true
	}
	return "", false
}

// dropUnused forgets a stored package once no extension runs from it.
func (m *Manager) dropUnused(source string) error {
	key, ok := packageKey(source)
	if !ok {
		return nil
	}
	m.mu.RLock()
	used := slices.ContainsFunc(m.instances, func(i *instance) bool {
		k, _ := packageKey(i.entry.Source)
		return k == key
	})
	m.mu.RUnlock()
	if used {
		return nil
	}
	return m.dropUpload(key)
}

// checkMarket makes sure the code in a market package is the id and version its source claims.
func checkMarket(source string, l *loaded) error {
	match := marketSource.FindStringSubmatch(source)
	if match == nil {
		return nil
	}
	if l.ID != match[1] || l.Version != match[2] {
		return fmt.Errorf("this package says it is %q %s, not %q %s as the marketplace listed", l.ID, l.Version, match[1], match[2])
	}
	return nil
}
