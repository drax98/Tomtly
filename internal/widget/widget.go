// Package widget checks what a script or an extension says a widget on Home shows. A widget is data, never markup:
// the page draws each block in its own style, so a package can't run code in the browser.
package widget

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/dop251/goja"
)

const (
	maxBlocks  = 40
	maxRows    = 50
	maxOptions = 50
	maxText    = 1000
	maxString  = 200
	maxArgs    = 2048
	// MaxRendered is the most a widget's blocks may take as JSON.
	MaxRendered = 32 << 10
	// MaxInput is the most a click may send back.
	MaxInput = 4096
)

// Name is a widget's key, an input's name or an action's.
var Name = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,39}$`)

// Row is one line of a rows block.
type Row struct {
	Label string          `json:"label"`
	Value json.RawMessage `json:"value,omitempty"`
	Tone  string          `json:"tone,omitempty"`
}

// Block is one piece of a widget. Which fields mean anything depends on Type; see sdk/home.d.ts.
type Block struct {
	Type    string          `json:"type"`
	Label   string          `json:"label,omitempty"`
	Text    string          `json:"text,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Unit    string          `json:"unit,omitempty"`
	Tone    string          `json:"tone,omitempty"`
	Rows    []Row           `json:"rows,omitempty"`
	Device  string          `json:"device,omitempty"`
	Code    string          `json:"code,omitempty"`
	Name    string          `json:"name,omitempty"`
	Min     *float64        `json:"min,omitempty"`
	Max     *float64        `json:"max,omitempty"`
	Step    *float64        `json:"step,omitempty"`
	Options []string        `json:"options,omitempty"`
	Action  string          `json:"action,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
}

var tones = map[string]bool{"": true, "good": true, "warn": true, "bad": true}

// Clean checks what render returned, a list of blocks, and writes it again with only the fields the page knows.
func Clean(raw []byte) (json.RawMessage, error) {
	var blocks []Block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, errors.New("render must return a list of blocks")
	}
	if len(blocks) > maxBlocks {
		return nil, fmt.Errorf("render returned %d blocks, and a widget holds up to %d", len(blocks), maxBlocks)
	}
	for i := range blocks {
		if err := check(&blocks[i]); err != nil {
			return nil, fmt.Errorf("block %d: %w", i+1, err)
		}
	}
	if blocks == nil {
		blocks = []Block{}
	}
	out, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxRendered {
		return nil, fmt.Errorf("render returned over %d bytes", MaxRendered)
	}
	return out, nil
}

func check(b *Block) error {
	for _, s := range []string{b.Label, b.Unit, b.Device, b.Code} {
		if utf8.RuneCountInString(s) > maxString {
			return fmt.Errorf("a label, unit or id is up to %d characters", maxString)
		}
	}
	if utf8.RuneCountInString(b.Text) > maxText {
		return fmt.Errorf("text is up to %d characters", maxText)
	}
	if !tones[b.Tone] {
		return errors.New(`tone is "good", "warn" or "bad"`)
	}
	if !scalar(b.Value) {
		return errors.New("value is a string, number or boolean")
	}
	switch b.Type {
	case "text":
		if b.Text == "" {
			return errors.New("a text block needs text")
		}
	case "stat":
		if b.Label == "" {
			return errors.New("a stat needs a label")
		}
	case "rows":
		if len(b.Rows) > maxRows {
			return fmt.Errorf("up to %d rows", maxRows)
		}
		for _, r := range b.Rows {
			if utf8.RuneCountInString(r.Label) > maxString || !scalar(r.Value) || !tones[r.Tone] {
				return fmt.Errorf("a row is a label of up to %d characters, a string, number or boolean value, and a tone", maxString)
			}
		}
	case "device":
		if b.Device == "" || b.Code == "" {
			return errors.New("a device block needs device and code")
		}
	case "number", "toggle", "select":
		if !Name.MatchString(b.Name) {
			return errors.New("an input needs a name: letters, digits, - and _, starting with a letter")
		}
		if b.Type == "select" && (len(b.Options) == 0 || len(b.Options) > maxOptions) {
			return fmt.Errorf("a select needs 1 to %d options", maxOptions)
		}
		for _, o := range b.Options {
			if utf8.RuneCountInString(o) > maxString {
				return fmt.Errorf("an option is up to %d characters", maxString)
			}
		}
	case "button":
		if b.Label == "" || !Name.MatchString(b.Action) {
			return errors.New("a button needs a label and an action: letters, digits, - and _, starting with a letter")
		}
		if len(b.Args) > 0 && (len(b.Args) > maxArgs || !bytes.HasPrefix(bytes.TrimSpace(b.Args), []byte("{"))) {
			return fmt.Errorf("args is an object of up to %d bytes", maxArgs)
		}
	default:
		return fmt.Errorf("unknown type %q: use text, stat, rows, device, number, toggle, select or button", b.Type)
	}
	return nil
}

func scalar(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	return len(v) == 0 || (v[0] != '{' && v[0] != '[')
}

// HasAction says whether a button in the rendered blocks asks for this action. A click may only ask for what the
// widget shows, so a widget can't be used to reach an extension's other actions.
func HasAction(rendered json.RawMessage, action string) bool {
	var blocks []Block
	if json.Unmarshal(rendered, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "button" && b.Action == action {
			return true
		}
	}
	return false
}

// Input checks what a click sent: a JSON object, small.
func Input(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}"), nil
	}
	var object map[string]any
	if len(raw) > MaxInput || json.Unmarshal(raw, &object) != nil {
		return nil, fmt.Errorf("input is an object of up to %d bytes", MaxInput)
	}
	return raw, nil
}

// Export is v.Export() for a property that may be missing: goja's Get gives nil then, and calling Export on that
// would take the service down.
func Export(v goja.Value) any {
	if v == nil {
		return nil
	}
	return v.Export()
}

// Rendered is a widget as the page gets it.
type Rendered struct {
	// Key is "<owner>/<key>": owner is builtin, script:<name> or ext:<id>.
	Key    string          `json:"key"`
	Title  string          `json:"title"`
	From   string          `json:"from"`
	Blocks json.RawMessage `json:"blocks,omitempty"`
	Error  string          `json:"error,omitempty"`
	// Practising is a script in shadow mode: a click logs what it would do and changes nothing.
	Practising bool `json:"practising,omitempty"`
}
