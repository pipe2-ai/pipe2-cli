package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Out prints either a JSON encoding of v (when --json) to stdout, or a
// human-readable form via humanize() to stdout. Human progress messages
// always go to stderr via Status() so that an agent piping stdout never
// has to filter them out.
func Out(v any) error {
	if Globals.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return humanize(os.Stdout, v)
}

// OutNDJSON streams one JSON object per line. Used for paginated lists and
// long-running watches so agents can process incrementally.
func OutNDJSON(items <-chan any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	for item := range items {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

// Status prints a human-readable progress line to stderr. Suppressed when
// --json is set unless --verbose is also set, because agents tend to ignore
// stderr but we still want it visible during interactive debugging.
func Status(format string, args ...any) {
	if Globals.JSON && !Globals.Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// humanize is the fallback text renderer. We deliberately keep it dumb —
// each command can override formatting by detecting !Globals.JSON itself
// and printing whatever it wants. This is the catch-all.
func humanize(w io.Writer, v any) error {
	switch t := v.(type) {
	case string:
		_, err := fmt.Fprintln(w, t)
		return err
	case fmt.Stringer:
		_, err := fmt.Fprintln(w, t.String())
		return err
	default:
		// Pretty-print as indented JSON for unknown types so humans still
		// get something readable.
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
}
