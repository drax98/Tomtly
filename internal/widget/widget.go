// Package widget checks what a script or an extension says a widget on Home shows. A widget is data, never markup:
// the page draws each block in its own style, so a package can't run code in the browser.
package widget

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/dop251/goja"
)

const (
	maxBlocks  = 40
	maxRows    = 50
	maxOptions = 50
	maxPeriods = 12
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

// Period is one row of a schedule: when it starts, "HH:MM", and the temperature it holds. Either may be unknown yet.
type Period struct {
	Time string   `json:"time,omitempty"`
	Temp *float64 `json:"temp,omitempty"`
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
	// Title and Blocks belong to a dialog: its heading and the form it opens.
	Title  string  `json:"title,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`
	// Periods and Now belong to a schedule: its rows, and which of them runs now.
	Periods []Period `json:"periods,omitempty"`
	Now     *int     `json:"now,omitempty"`
}

var tones = map[string]bool{"": true, "good": true, "warn": true, "bad": true}

// clock is a time input's value, "HH:MM".
var clock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// inDialog is what a dialog's form may hold: words and inputs, never a device, a button or another dialog.
var inDialog = map[string]bool{"group": true, "text": true, "rows": true, "number": true, "toggle": true, "select": true, "time": true, "schedule": true, "choice": true}

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
		if blocks[i].Type == "schedule" {
			return nil, fmt.Errorf("block %d: a schedule goes in a dialog", i+1)
		}
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
	for _, s := range []string{b.Label, b.Unit, b.Device, b.Code, b.Title} {
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
	case "group":
		if b.Label == "" {
			return errors.New("a group needs a label")
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
		if b.Min != nil && b.Max != nil && *b.Min >= *b.Max {
			return errors.New("a device block's min is below its max")
		}
	case "number", "toggle", "select", "time":
		if !Name.MatchString(b.Name) {
			return errors.New("an input needs a name: letters, digits, - and _, starting with a letter")
		}
		if err := checkTime(b); err != nil {
			return err
		}
		if b.Type == "select" && (len(b.Options) == 0 || len(b.Options) > maxOptions) {
			return fmt.Errorf("a select needs 1 to %d options", maxOptions)
		}
		for _, o := range b.Options {
			if utf8.RuneCountInString(o) > maxString {
				return fmt.Errorf("an option is up to %d characters", maxString)
			}
		}
	case "schedule":
		if err := checkSchedule(b); err != nil {
			return err
		}
	case "choice":
		if err := checkChoice(b); err != nil {
			return err
		}
	case "button":
		return checkPress(b, "a button")
	case "dialog":
		if err := checkPress(b, "a dialog"); err != nil {
			return err
		}
		return checkForm(b.Blocks)
	default:
		return fmt.Errorf("unknown type %q: use group, text, stat, rows, device, number, toggle, select, time, schedule, choice, button or dialog", b.Type)
	}
	if len(b.Blocks) > 0 {
		return errors.New("only a dialog holds blocks")
	}
	return nil
}

// checkSchedule checks a day's program. It doesn't check the periods are in order: a device can hold a program out of
// order, and refusing it would break the whole widget, so the page shows it and asks for a fix.
func checkSchedule(b *Block) error {
	if !Name.MatchString(b.Name) || b.Label == "" {
		return errors.New("a schedule needs a name: letters, digits, - and _, starting with a letter, and a label")
	}
	if len(b.Periods) == 0 || len(b.Periods) > maxPeriods {
		return fmt.Errorf("a schedule holds 1 to %d periods", maxPeriods)
	}
	ranged := b.Min != nil && b.Max != nil
	if ranged && *b.Min >= *b.Max {
		return errors.New("a schedule's min is below its max")
	}
	for i, p := range b.Periods {
		if p.Time != "" && !clock.MatchString(p.Time) {
			return fmt.Errorf(`period %d: a time is "HH:MM"`, i+1)
		}
		if p.Temp == nil {
			continue
		}
		if *p.Temp != float64(int(*p.Temp)) || ranged && (*p.Temp < *b.Min || *p.Temp > *b.Max) {
			return fmt.Errorf("period %d: a temperature is a whole number from min to max", i+1)
		}
	}
	if b.Now != nil && (*b.Now < 0 || *b.Now >= len(b.Periods)) {
		return errors.New("a schedule's now is the index of one of its periods")
	}
	return checkStep(b.Step)
}

// checkChoice checks a choice: a pressable with a name, and 2 to 4 options its value is one of.
func checkChoice(b *Block) error {
	if err := checkPress(b, "a choice"); err != nil {
		return err
	}
	if !Name.MatchString(b.Name) {
		return errors.New("a choice needs a name: letters, digits, - and _, starting with a letter")
	}
	if len(b.Options) < 2 || len(b.Options) > 4 {
		return errors.New("a choice needs 2 to 4 options")
	}
	for _, o := range b.Options {
		if utf8.RuneCountInString(o) > maxString {
			return fmt.Errorf("an option is up to %d characters", maxString)
		}
	}
	var value string
	if len(b.Value) > 0 && (json.Unmarshal(b.Value, &value) != nil || !slices.Contains(b.Options, value)) {
		return errors.New("a choice's value is one of its options")
	}
	return nil
}

// checkPress checks what a button and a dialog share: a label, and an action with optional args.
func checkPress(b *Block, what string) error {
	if b.Label == "" || !Name.MatchString(b.Action) {
		return fmt.Errorf("%s needs a label and an action: letters, digits, - and _, starting with a letter", what)
	}
	if len(b.Args) > 0 && (len(b.Args) > maxArgs || !bytes.HasPrefix(bytes.TrimSpace(b.Args), []byte("{"))) {
		return fmt.Errorf("args is an object of up to %d bytes", maxArgs)
	}
	return nil
}

// checkForm checks a dialog's blocks: 1 to maxBlocks of what inDialog allows.
func checkForm(blocks []Block) error {
	if len(blocks) == 0 || len(blocks) > maxBlocks {
		return fmt.Errorf("a dialog holds 1 to %d blocks", maxBlocks)
	}
	for i := range blocks {
		if !inDialog[blocks[i].Type] {
			return fmt.Errorf("a dialog can't hold a %q block", blocks[i].Type)
		}
		if err := check(&blocks[i]); err != nil {
			return fmt.Errorf("dialog block %d: %w", i+1, err)
		}
	}
	return nil
}

// checkTime checks a time input: its value, if given, is "HH:MM", and its step is whole minutes.
func checkTime(b *Block) error {
	if b.Type != "time" {
		return nil
	}
	var value string
	if len(b.Value) > 0 && (json.Unmarshal(b.Value, &value) != nil || !clock.MatchString(value)) {
		return errors.New(`a time's value is "HH:MM"`)
	}
	return checkStep(b.Step)
}

// checkStep checks a time's or a schedule's step: whole minutes.
func checkStep(step *float64) error {
	if step != nil && (*step < 1 || *step > 720 || *step != float64(int(*step))) {
		return errors.New("a step is whole minutes, 1 to 720")
	}
	return nil
}

func scalar(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	return len(v) == 0 || (v[0] != '{' && v[0] != '[')
}

// HasAction says whether a button, a dialog or a choice in the rendered blocks, or in a dialog's form, asks for this
// action. A click may only ask for what the widget shows, so a widget can't be used to reach an extension's other actions.
func HasAction(rendered json.RawMessage, action string) bool {
	var blocks []Block
	if json.Unmarshal(rendered, &blocks) != nil {
		return false
	}
	return hasAction(blocks, action)
}

var presses = map[string]bool{"button": true, "dialog": true, "choice": true}

func hasAction(blocks []Block, action string) bool {
	for _, b := range blocks {
		if (presses[b.Type] && b.Action == action) || hasAction(b.Blocks, action) {
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
