package extension

import (
	"strconv"
	"strings"
)

// newerVersion compares semantic versions. One that isn't, on either side, is never called newer.
func newerVersion(candidate, have string) bool {
	a, ok1 := parseVersion(candidate)
	b, ok2 := parseVersion(have)
	if !ok1 || !ok2 {
		return false
	}
	for n := range a.nums {
		if a.nums[n] != b.nums[n] {
			return a.nums[n] > b.nums[n]
		}
	}
	switch { // a release is newer than its own pre-release
	case a.pre == b.pre:
		return false
	case a.pre == "":
		return true
	case b.pre == "":
		return false
	}
	return comparePre(a.pre, b.pre) > 0
}

type version struct {
	nums [3]int
	pre  string
}

func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	v := version{pre: pre}
	for n, p := range parts {
		num, err := strconv.Atoi(p)
		if err != nil || num < 0 {
			return version{}, false
		}
		v.nums[n] = num
	}
	return v, true
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for n := 0; n < len(as) && n < len(bs); n++ {
		x, ex := strconv.Atoi(as[n])
		y, ey := strconv.Atoi(bs[n])
		switch {
		case ex == nil && ey == nil && x != y:
			return x - y
		case ex == nil && ey != nil:
			return -1 // numbers rank below words
		case ex != nil && ey == nil:
			return 1
		case ex != nil && ey != nil && as[n] != bs[n]:
			return strings.Compare(as[n], bs[n])
		}
	}
	return len(as) - len(bs)
}
