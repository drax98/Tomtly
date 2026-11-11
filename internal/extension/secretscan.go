package extension

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

const (
	// fileChunk is the size of the stretch of a file that is looked for. A run of twice this is always found, and half of
	// that sometimes is. It's well over what an extension repeats from a file by publishing its devices' names, ids and choices.
	fileChunk = 256
	// unpackDepth is how many levels of JSON inside a JSON string are looked into.
	unpackDepth = 4
)

// broadcast is who a broadcast is sent to. It can't be a host in sendTo, since it isn't one by hostFormat.
const broadcast = "every host"

// jsonEscaped is text as JSON.stringify writes it, inside the quotes.
func jsonEscaped(text string) string {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(text) != nil {
		return text
	}
	return out.String()[1 : out.Len()-2] // the quotes, and the newline Encode adds
}

// compactJSON is a JSON file as JSON.stringify writes it after JSON.parse, or "" if it isn't JSON.
// shortcut: non-ASCII escaped in the file (ł) stays escaped here, which only breaks a run where it occurs; decode it if files turn out mostly non-ASCII.
func compactJSON(text string) string {
	var out bytes.Buffer
	if json.Compact(&out, []byte(text)) != nil {
		return ""
	}
	return out.String()
}

// fileTexts is what counts as a file's whole text. Anything shorter than a secret field would match ordinary output.
func fileTexts(text string) []string {
	texts := slices.Compact([]string{text, strings.TrimSpace(text), compactJSON(text)})
	return slices.DeleteFunc(texts, func(t string) bool { return len(t) < minSecretField })
}

// chunksOf splits a file into pieces of fileChunk bytes, as it is, re-serialised, and with JSON's escapes.
func chunksOf(text string) map[string]bool {
	chunks := map[string]bool{}
	compact := compactJSON(text)
	for _, written := range []string{text, compact, jsonEscaped(text), jsonEscaped(compact)} {
		for start := 0; start+fileChunk <= len(written); start += fileChunk {
			chunks[written[start:start+fileChunk]] = true
		}
	}
	return chunks
}

// holdsChunk is whether text has one of the file's pieces in it, at any offset.
// shortcut: hashes 256 bytes at every offset, about 30 ms for a 1 MiB result; use a rolling hash if it shows in a profile.
func (b binding) holdsChunk(text string) bool {
	if len(b.chunks) == 0 {
		return false
	}
	for start := 0; start+fileChunk <= len(text); start++ {
		if b.chunks[text[start:start+fileChunk]] {
			return true
		}
	}
	return false
}

// unpacked is text, and if it's JSON every string and key in it, since JSON's escapes hide a secret from a plain search.
func unpacked(text string) []string {
	out := []string{}
	var add func(text string, depth int)
	var walk func(v any, depth int)
	add = func(text string, depth int) {
		out = append(out, text)
		trimmed := strings.TrimLeft(text, " \t\r\n")
		if depth == 0 || trimmed == "" || !strings.ContainsRune(`{["`, rune(trimmed[0])) {
			return
		}
		var doc any
		if json.Unmarshal([]byte(trimmed), &doc) == nil {
			walk(doc, depth-1)
		}
	}
	walk = func(v any, depth int) {
		switch v := v.(type) {
		case string:
			add(v, depth)
		case []any:
			for _, item := range v {
				walk(item, depth)
			}
		case map[string]any:
			for key, item := range v {
				add(key, depth)
				walk(item, depth)
			}
		}
	}
	add(text, unpackDepth)
	return out
}

// holdsSecret is whether any secret setting is in text, in a form it knows, wherever it may otherwise go.
func (i *instance) holdsSecret(text string) bool {
	i.mu.RLock()
	bindings := i.bindings
	i.mu.RUnlock()
	if len(bindings) == 0 {
		return false
	}
	pieces := unpacked(text)
	return slices.ContainsFunc(bindings, func(b binding) bool { return slices.ContainsFunc(pieces, b.appearsIn) })
}

// holdsFile is whether text as it stands holds a file's whole text or a stretch of it.
func (i *instance) holdsFile(text string) bool {
	i.mu.RLock()
	bindings := i.bindings
	i.mu.RUnlock()
	return slices.ContainsFunc(bindings, func(b binding) bool { return b.whole && b.appearsIn(text) })
}
