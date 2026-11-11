package extension

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// The scan reads the bundled JavaScript for what an honest extension has no reason to contain. It only warns: the owner
// decides. It is a hint and not a defence, because the runtime gates and the removal of eval and Function are what
// hold; those make the patterns below meaningful, since code can't build the dangerous call at run time instead.
var (
	stringLiteral = regexp.MustCompile("\"(?:[^\"\\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\\n]|\\\\.)*'|`(?:[^`\\\\]|\\\\.)*`")
	webAddress    = regexp.MustCompile("(?i)https?://([^\\s/\"'`<>?#\\\\]*)")
	bareHost      = regexp.MustCompile(`^(?i:[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}|[0-9]{1,3}(?:\.[0-9]{1,3}){3})(?::[0-9]+)?$`)
	encodedBlob   = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{500,}$`)
	charCodes     = regexp.MustCompile(`fromCharCode\s*\(([^)]*)\)`)
	codeChecks    = []struct {
		pattern *regexp.Regexp
		found   string
	}{
		{regexp.MustCompile(`\beval\s*\(`), "it calls eval(, which runs code built at run time"},
		{regexp.MustCompile(`\bFunction\s*\(`), "it calls Function(, which builds code at run time"},
		{regexp.MustCompile(`constructor\s*(?:\.|\[\s*["'])\s*constructor`), "it uses constructor.constructor, a way to build code at run time"},
		{regexp.MustCompile(`__proto__`), "it uses __proto__, which can change how other objects behave"},
	}
	// Names that look like hosts and are files.
	fileSuffixes = []string{"json", "js", "ts", "css", "html", "txt", "png", "jpg", "svg", "map", "md", "yaml", "yml", "xml"}
)

// scan lists what in the code deserves a person's attention.
func scan(code string, network []string, homepage string) []string {
	var warnings []string
	add := func(format string, args ...any) {
		if w := fmt.Sprintf(format, args...); !slices.Contains(warnings, w) {
			warnings = append(warnings, w)
		}
	}
	for _, c := range codeChecks {
		if c.pattern.MatchString(code) {
			add("The code is suspicious: %s.", c.found)
		}
	}
	for _, m := range charCodes.FindAllStringSubmatch(code, -1) {
		if strings.Count(m[1], ",") >= 10 {
			add("The code is suspicious: it builds text from a long list of character codes (String.fromCharCode), which can hide what it does.")
		}
	}
	allowed := map[string]bool{}
	for _, entry := range network {
		allowed[strings.ToLower(bareName(entry))] = true
	}
	if u, err := url.Parse(homepage); err == nil && u.Hostname() != "" {
		allowed[strings.ToLower(u.Hostname())] = true // a link shown on the page, not contacted
	}
	for _, literal := range stringLiteral.FindAllString(code, -1) {
		text := literal[1 : len(literal)-1]
		if encodedBlob.MatchString(text) {
			add("The code holds a very long encoded string (%d characters), which could be hidden code or data.", len(text))
		}
		for _, m := range webAddress.FindAllStringSubmatch(text, -1) {
			host := strings.ToLower(bareName(m[1]))
			if host != "" && !strings.ContainsAny(host, "${}") && !allowed[host] {
				add("The code mentions the address %s, which is not in its network access.", host)
			}
		}
		host := strings.ToLower(bareName(text))
		if bareHost.MatchString(text) && !allowed[host] && !fileName(host) {
			add("The code mentions the host %s, which is not in its network access.", host)
		}
	}
	return warnings
}

func fileName(host string) bool {
	return slices.ContainsFunc(fileSuffixes, func(s string) bool { return strings.HasSuffix(host, "."+s) })
}
