package extension

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Categories are the marketplace's filters. The list is fixed here, so an author can't make up a new one.
var Categories = []string{"climate", "lighting", "cleaning", "energy", "security", "sensors", "media", "weather", "bridges", "other"}

// Licenses are the open licenses the marketplace accepts, by SPDX id. Every listed version is open source under one of them.
var Licenses = []string{"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "MPL-2.0", "LGPL-2.1-only", "LGPL-2.1-or-later",
	"LGPL-3.0-only", "LGPL-3.0-or-later", "GPL-2.0-only", "GPL-2.0-or-later", "GPL-3.0-only", "GPL-3.0-or-later", "AGPL-3.0-only",
	"AGPL-3.0-or-later", "EUPL-1.2", "0BSD", "Unlicense"}

const (
	maxTags      = 20
	maxTagLength = 40
)

// checkListingWords is what an extension may say about itself for search: categories from the fixed list, and a few short
// tags, such as the brands and devices it works with.
func checkListingWords(categories, tags []string, license string) error {
	if license != "" && !slices.Contains(Licenses, license) {
		return fmt.Errorf("license %q isn't one of the open licenses: %s", clipText(license, 40), strings.Join(Licenses, ", "))
	}
	if len(categories) > len(Categories) {
		return fmt.Errorf("categories: at most %d", len(Categories))
	}
	for _, c := range categories {
		if !slices.Contains(Categories, c) {
			return fmt.Errorf("category %q isn't one of %s", clipText(c, 40), strings.Join(Categories, ", "))
		}
	}
	if len(tags) > maxTags {
		return fmt.Errorf("tags: at most %d", maxTags)
	}
	for _, k := range tags {
		if strings.TrimSpace(k) == "" || utf8.RuneCountInString(k) > maxTagLength || strings.ContainsFunc(k, func(r rune) bool { return r < ' ' }) {
			return fmt.Errorf("tag %q must be 1 to %d characters on one line", clipText(k, 40), maxTagLength)
		}
	}
	return nil
}

// MarketSorts are the orders the marketplace's search can sort in.
var MarketSorts = []string{"relevance", "downloads", "rating", "reviews", "newest", "updated"}
