package cookbook

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolveInputs validates user-supplied flag values against the
// recipe's declared inputs and returns the canonical map keyed by
// Input.Name with values typed per the declared Input.Type. Defaults
// applied for absent inputs. Required inputs missing from supplied
// produce an actionable error against their --cli flag.
//
// supplied is the raw map of cli-flag (--source, --preset, …) to
// string values, as parsed by the CLI's dynamic flag walker.
func ResolveInputs(m Manifest, supplied map[string]string) (map[string]any, error) {
	out := map[string]any{}

	// Lookup by CLI flag → Input.Name for the user side.
	byCLI := map[string]Input{}
	byName := map[string]Input{}
	for _, in := range m.Inputs {
		byName[in.Name] = in
		byCLI[trimDashes(in.CLIFlag())] = in
	}

	// Apply defaults first; user values win below.
	for _, in := range m.Inputs {
		if in.Default != nil {
			out[in.Name] = in.Default
		}
	}

	for k, raw := range supplied {
		key := trimDashes(k)
		decl, ok := byCLI[key]
		if !ok {
			// Allow addressing by name as a convenience.
			if d2, ok2 := byName[key]; ok2 {
				decl = d2
			} else {
				return nil, fmt.Errorf("unknown input %q (known flags: %s)", k, strings.Join(allFlags(m), ", "))
			}
		}
		val, err := coerce(raw, decl)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", decl.CLIFlag(), err)
		}
		out[decl.Name] = val
	}

	for _, in := range m.Inputs {
		if _, present := out[in.Name]; !present && in.Required {
			d := in.Description
			if d == "" {
				d = string(in.Type)
			}
			return nil, fmt.Errorf("%s is required (%s)", in.CLIFlag(), d)
		}
	}
	return out, nil
}

func coerce(raw string, decl Input) (any, error) {
	switch decl.Type {
	case String, AssetURL:
		return raw, nil
	case Enum:
		for _, v := range decl.Values {
			if raw == v {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("must be one of %v", decl.Values)
	case Int:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("not an integer: %q", raw)
		}
		f := float64(n)
		if decl.Min != nil && f < *decl.Min {
			return nil, fmt.Errorf("must be >= %v", *decl.Min)
		}
		if decl.Max != nil && f > *decl.Max {
			return nil, fmt.Errorf("must be <= %v", *decl.Max)
		}
		return n, nil
	case Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("not a boolean: %q (use true/false)", raw)
		}
		return b, nil
	default:
		return raw, nil
	}
}

func trimDashes(s string) string { return strings.TrimLeft(s, "-") }

func allFlags(m Manifest) []string {
	out := make([]string, 0, len(m.Inputs))
	for _, in := range m.Inputs {
		out = append(out, in.CLIFlag())
	}
	return out
}
