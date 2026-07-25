package cookbook

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ParseCorrections turns a CLI-friendly "from=to,from=to" string into a
// substitution map. Whitespace around each side of the "=" is trimmed
// so users can write `--corrections "Qube=Cube, Kyub=Cube"` without
// the leading space leaking into the regex.
//
// Returns nil + a descriptive error if any pair is malformed — the
// caller should fail the recipe before transcription runs, not
// silently drop the bad pair.
func ParseCorrections(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.Index(pair, "=")
		if eq < 1 || eq == len(pair)-1 {
			return nil, fmt.Errorf("malformed correction %q: expected from=to", pair)
		}
		from := strings.TrimSpace(pair[:eq])
		to := strings.TrimSpace(pair[eq+1:])
		if from == "" || to == "" {
			return nil, fmt.Errorf("malformed correction %q: from/to may not be empty", pair)
		}
		out[from] = to
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ApplyCorrections rewrites every match of each `from` key with the
// corresponding `to` value in body. Matches are case-sensitive and
// word-boundary-anchored. Longer keys apply first (so "Cube Cloud"
// rewrites before bare "Cube"). See go-shared/subtitles.ApplyCorrections
// for the canonical implementation used by the worker — this is a
// local duplicate (rather than an import) because pipe2-cli cross-
// compiles with CGO_ENABLED=0 and importing any other go-shared
// package transitively pulls in billing → tigerbeetle-go (CGO-only),
// breaking the build. Behavior must stay byte-identical to the
// shared version; the cookbook test suite pins both.
func ApplyCorrections(body string, corrections map[string]string) string {
	if len(corrections) == 0 || body == "" {
		return body
	}
	keys := make([]string, 0, len(corrections))
	for k := range corrections {
		keys = append(keys, k)
	}
	// Longest keys first, ties broken alphabetically.
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		return len(a) > len(b) || (len(a) == len(b) && a < b)
	})
	for _, from := range keys {
		re, err := regexp.Compile(`\b` + regexp.QuoteMeta(from) + `\b`)
		if err != nil {
			continue
		}
		body = re.ReplaceAllString(body, corrections[from])
	}
	return body
}
