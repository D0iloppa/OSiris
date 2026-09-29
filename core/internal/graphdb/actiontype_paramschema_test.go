package graphdb

// Unit tests for ValidateParametersSchemaJSON (80 §4.4). Present as
// documentation of intended behavior; this repository's installed Go
// (1.13.8) cannot compile or run this file against this module's go.mod
// (requires 1.22) — see this task's final report's "컴파일 미검증" note.
// Written to actually compile under Go 1.22 nonetheless, so whoever next
// has a working toolchain can run `go test ./internal/graphdb/...` and get
// real signal, not just documentation value.

import "testing"

func TestValidateParametersSchemaJSON_Accepts(t *testing.T) {
	cases := []string{
		`{"type":"object","properties":{}}`,
		`{"type":"object","properties":{"max":{"type":"integer","minimum":1,"maximum":100000,"default":1000,"description":"upper bound"}},"required":["max"]}`,
		`{"type":"string","enum":["a","b","c"]}`,
		`{"type":"array","items":{"type":"string","maxLength":40},"minItems":0,"maxItems":10}`,
		`{"type":"string","pattern":"^[a-z-]+$","maxLength":64}`,
		`{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"string","maxLength":8}}}}}}}`, // depth 3, at the boundary
		`{"type":"object","properties":{},"additionalProperties":false}`,
	}
	for _, c := range cases {
		if err := ValidateParametersSchemaJSON(c); err != nil {
			t.Errorf("expected %s to be accepted, got error: %v", c, err)
		}
	}
}

func TestValidateParametersSchemaJSON_Rejects(t *testing.T) {
	cases := map[string]string{
		"empty":                       "",
		"not json":                    "{not json}",
		"trailing data":               `{"type":"object","properties":{}}{}`,
		"$ref":                        `{"$ref":"#/definitions/foo"}`,
		"$schema":                     `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
		"oneOf":                       `{"oneOf":[{"type":"string"},{"type":"integer"}]}`,
		"if/then/else":                `{"if":{"type":"string"},"then":{"maxLength":5}}`,
		"patternProperties":           `{"patternProperties":{"^S_":{"type":"string"}}}`,
		"additionalProperties schema": `{"type":"object","additionalProperties":{"type":"string"}}`,
		"format":                      `{"type":"string","format":"email"}`,
		"unknown keyword":             `{"type":"string","const":"x"}`,
		"unknown top-level type":      `{"type":"null"}`,
		"pattern without maxLength":   `{"type":"string","pattern":"^[a-z]+$"}`,
		"pattern too long":            `{"type":"string","pattern":"` + repeatA(300) + `","maxLength":10}`,
		"depth 4":                     `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"object","properties":{"d":{"type":"string","maxLength":1}}}}}}}}}`,
		"items tuple form":            `{"type":"array","items":[{"type":"string"},{"type":"integer"}]}`,
	}
	for name, c := range cases {
		if err := ValidateParametersSchemaJSON(c); err == nil {
			t.Errorf("%s: expected rejection for %s, got nil error", name, c)
		}
	}
}

func repeatA(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
