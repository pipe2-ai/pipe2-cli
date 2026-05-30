package cookbook

import (
	"encoding/json"
	"sort"
)

// MarshalJSON renders Manifest in the shape the website's content
// loader (packages/web/src/content.config.ts) reads. Inputs are
// serialized as a map<string, InputDecl> rather than an array so
// the Zod schema's z.record(inputDecl) matches without translation.
//
// Custom MarshalJSON keeps the in-memory representation ergonomic
// for recipes (Input slices preserve declaration order — useful for
// CLI --help output) while emitting a JSON shape that's natural for
// the web.
func (m Manifest) MarshalJSON() ([]byte, error) {
	type alias Manifest // dodge the recursive marshal trap

	// Build the inputs object preserving declaration order via the
	// authoring slice. JSON object keys are unordered in spec, but
	// most consumers (web codegen, agents reading manifest.json)
	// follow source order — Go's map iteration would scramble it,
	// so we marshal by hand.
	if len(m.Inputs) == 0 {
		return json.Marshal(alias(m))
	}

	a := alias(m)
	aBytes, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}

	// Splice an `inputs` field into the marshaled object. Sort by
	// Input.Name for deterministic CI output across machines/Go
	// versions; the article reads them by-name anyway.
	inputs := make([]Input, len(m.Inputs))
	copy(inputs, m.Inputs)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })

	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(aBytes, &obj); err != nil {
		return nil, err
	}
	inputsObj := map[string]Input{}
	for _, in := range inputs {
		inputsObj[in.Name] = in
	}
	inputsBytes, err := json.Marshal(inputsObj)
	if err != nil {
		return nil, err
	}
	obj["inputs"] = inputsBytes
	return json.Marshal(obj)
}

// FillSchemaVersion sets schema_version=SchemaVersion when the
// manifest doesn't have one. Recipes typically don't bother setting
// it; the generator calls this before serializing.
func (m *Manifest) FillSchemaVersion() {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = SchemaVersion
	}
}
