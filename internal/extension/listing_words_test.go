package extension

import (
	"fmt"
	"strings"
	"testing"
)

func TestListingWords(t *testing.T) {
	for _, c := range []struct {
		categories, tags []string
		ok               bool
	}{
		{[]string{"climate", "energy"}, []string{"Gree", "Sinclair AC"}, true},
		{nil, nil, true},
		{[]string{"toys"}, nil, false},
		{nil, []string{" "}, false},
		{nil, []string{"line\nbreak"}, false},
		{nil, []string{strings.Repeat("ż", maxTagLength)}, true},
		{nil, []string{strings.Repeat("ż", maxTagLength+1)}, false},
		{nil, make([]string, maxTags+1), false},
	} {
		if err := checkListingWords(c.categories, c.tags, ""); (err == nil) != c.ok {
			t.Errorf("%v %v: %v", c.categories, fmt.Sprintf("%q", c.tags), err)
		}
	}
}
