package extension

import "io"

// Extract unpacks a GitHub archive safely into a folder, for the marketplace.
func Extract(r io.Reader, into string) error { return extract(r, into) }

// NewerVersion says whether candidate is a later semantic version than have.
func NewerVersion(candidate, have string) bool { return newerVersion(candidate, have) }

// ValidVersion says whether a version can be compared at all. NewerVersion is false for one that can't.
func ValidVersion(v string) bool {
	_, ok := parseVersion(v)
	return ok
}
