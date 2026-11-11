// Package typegen writes TypeScript types for this home's real devices and plugin data, so the script editor can suggest
// device ids, their value codes and allowed values, and each plugin's fields, without anyone writing types by hand.
package typegen

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"tomtly/device"
	"tomtly/plugin"
)

// Generate returns a .d.ts adding to the DeviceMap and PluginMap in sdk/home.d.ts. Plugins in declared bring their own
// types. Only shapes of plugin data appear here, never its values.
func Generate(devices []device.Device, plugins []plugin.Status, declared map[string]bool) string {
	var out strings.Builder
	out.WriteString("// Generated from this home's devices and plugins.\n\ninterface DeviceMap {\n")
	for _, d := range devices {
		fmt.Fprintf(&out, "  /** %s */\n  %s: TypedDevice<{\n", comment(strings.TrimSpace(d.Name+" ("+d.Kind+")")), quote(d.ID))
		for _, dp := range d.DPs {
			if dp.Type != device.Action {
				property(&out, dp)
			}
		}
		out.WriteString("  }, {\n")
		for _, dp := range d.DPs {
			if dp.Writable {
				property(&out, dp)
			}
		}
		out.WriteString("  }>;\n")
	}
	out.WriteString("}\n\ninterface PluginMap {\n")
	for _, p := range plugins {
		if declared[p.ID] {
			continue
		}
		shape := "unknown"
		if len(p.Data) > 0 {
			var data any
			if json.Unmarshal(p.Data, &data) == nil {
				n := &node{}
				n.add(data)
				shape = n.render("  ", 0)
			}
		}
		fmt.Fprintf(&out, "  %s: %s;\n", quote(p.ID), shape)
	}
	out.WriteString("}\n\ninterface PluginActionMap {\n")
	for _, p := range plugins {
		if len(p.Actions) > 0 {
			names := make([]string, len(p.Actions))
			for i, name := range p.Actions {
				names[i] = quote(name)
			}
			fmt.Fprintf(&out, "  %s: %s;\n", quote(p.ID), strings.Join(names, " | "))
		}
	}
	out.WriteString("}\n")
	return out.String()
}

func property(out *strings.Builder, dp device.DP) {
	if doc := describe(dp); doc != "" {
		fmt.Fprintf(out, "    /** %s */\n", comment(doc))
	}
	fmt.Fprintf(out, "    %s: %s;\n", quote(dp.Code), valueType(dp))
}

func valueType(dp device.DP) string {
	switch dp.Type {
	case device.Boolean:
		return "boolean"
	case device.Integer, "Number":
		return "number"
	case device.Enum, device.Action:
		if choices := device.Strings(dp.Values["range"]); len(choices) > 0 {
			quoted := make([]string, len(choices))
			for i, c := range choices {
				quoted[i] = quote(c)
			}
			return strings.Join(quoted, " | ")
		}
		return "string"
	case "String":
		return "string"
	}
	return "Value" // whatever else an adapter reports; it still can be read
}

func describe(dp device.DP) string {
	var parts []string
	lo, hasLo := device.Number(dp.Values["min"])
	hi, hasHi := device.Number(dp.Values["max"])
	if hasLo && hasHi {
		parts = append(parts, fmt.Sprintf("%v to %v", lo, hi))
	}
	if unit, ok := dp.Values["unit"].(string); ok && unit != "" {
		parts = append(parts, unit)
	}
	if dp.Scale > 0 {
		parts = append(parts, fmt.Sprintf("raw units: scale %d, so divide by %d", dp.Scale, pow10(dp.Scale)))
	}
	return strings.Join(parts, ", ")
}

func pow10(n int) int {
	out := 1
	for range n {
		out *= 10
	}
	return out
}

func quote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

// comment makes text from a device safe inside a doc comment, since a device's name could otherwise end it.
func comment(s string) string {
	s = strings.ReplaceAll(s, "*/", "* /")
	return strings.Join(strings.Fields(s), " ")
}

const maxDepth = 8

// node is the merged shape of every value seen at one place in the data, so a list's items combine into one type.
type node struct {
	seen       int
	primitives []string
	fields     map[string]*node
	order      []string
	items      *node
}

func (n *node) add(v any) {
	n.seen++
	switch x := v.(type) {
	case map[string]any:
		if n.fields == nil {
			n.fields = map[string]*node{}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			field, ok := n.fields[k]
			if !ok {
				field = &node{}
				n.fields[k] = field
				n.order = append(n.order, k)
			}
			field.add(x[k])
		}
	case []any:
		if n.items == nil {
			n.items = &node{}
		}
		for _, item := range x {
			n.items.add(item)
		}
	case string:
		n.primitive("string")
	case float64:
		n.primitive("number")
	case bool:
		n.primitive("boolean")
	case nil:
		n.primitive("null")
	}
}

func (n *node) primitive(name string) {
	if !slices.Contains(n.primitives, name) {
		n.primitives = append(n.primitives, name)
	}
}

func (n *node) render(indent string, depth int) string {
	if depth > maxDepth {
		return "any"
	}
	var types []string
	if n.fields != nil {
		var b strings.Builder
		b.WriteString("{\n")
		for _, k := range n.order {
			field := n.fields[k]
			optional := ""
			// A field missing from some items of a list may be missing from the next one too.
			if field.seen < n.objects() {
				optional = "?"
			}
			fmt.Fprintf(&b, "%s  %s%s: %s;\n", indent, quote(k), optional, field.render(indent+"  ", depth+1))
		}
		b.WriteString(indent + "}")
		types = append(types, b.String())
	}
	if n.items != nil {
		item := "unknown"
		if n.items.seen > 0 {
			item = n.items.render(indent, depth+1)
		}
		types = append(types, "("+item+")[]")
	}
	types = append(types, n.sortedPrimitives()...)
	if len(types) == 0 {
		return "unknown"
	}
	return strings.Join(types, " | ")
}

func (n *node) objects() int {
	most := 0
	for _, field := range n.fields {
		most = max(most, field.seen)
	}
	return most
}

// sortedPrimitives keeps null last, which reads naturally: number | null.
func (n *node) sortedPrimitives() []string {
	out := slices.Clone(n.primitives)
	slices.SortStableFunc(out, func(a, b string) int {
		switch {
		case a == "null" && b != "null":
			return 1
		case b == "null" && a != "null":
			return -1
		}
		return 0
	})
	return out
}
