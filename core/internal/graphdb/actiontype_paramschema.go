// parametersSchemaJson subset validator — docs/design/80-action-layer.md
// §4.4 (verbatim keyword lists) and §9 AC4/CA1 (this is part of "no field
// can carry an executable expression" — a schema language with $ref/oneOf/
// if-then-else IS a small interpreter, per §5.3.2's "표현 상한" argument).
//
// PURE FUNCTION, no graphdb/Neo4j dependency, by design — this file is
// separable and independently unit-testable (this task's brief: "별도
// 파일로 두고 유닛테스트 가능한 순수 함수로 만들어라"). It does not import
// anything from this package; it is colocated here only because its sole
// caller (actiontype.go's CreateActionType/UpdateActionType validation) is
// here. See actiontype_paramschema_test.go alongside it — present as
// documentation of intended behavior even though this repo's installed Go
// (1.13.8) cannot compile or run it against this module's go.mod (which
// requires 1.22); see this task's final report for the explicit
// "컴파일/테스트 미검증" callout.
//
// FAIL-CLOSED ON UNKNOWN KEYWORDS: §4.4 gives two lists — 허용
// (allowed) and 금지 (forbidden/rejected) — but does not claim the union of
// the two is exhaustive over JSON Schema's actual vocabulary (it explicitly
// is not: e.g. "$comment", "examples", "const", "propertyNames" appear in
// neither list). This validator therefore rejects ANY key not in the
// allowed list, not just the ones §4.4 names as forbidden — the task brief
// is explicit that an unlisted keyword must be treated as unknown and
// rejected, not silently permitted. This is the same fail-closed posture
// AC3/CA3 take toward unregistered predicate/effect names.
package graphdb

import (
	"encoding/json"
	"fmt"
	"regexp/syntax"
	"strings"
)

// parameterSchemaAllowedKeys is 80 §4.4's "허용" list, transcribed verbatim:
// "type, properties, required, enum, minimum/maximum, minLength/maxLength,
// minItems/maxItems, pattern, items, description, default".
var parameterSchemaAllowedKeys = map[string]struct{}{
	"type":        {},
	"properties":  {},
	"required":    {},
	"enum":        {},
	"minimum":     {},
	"maximum":     {},
	"minLength":   {},
	"maxLength":   {},
	"minItems":    {},
	"maxItems":    {},
	"pattern":     {},
	"items":       {},
	"description": {},
	"default":     {},
}

// parameterSchemaAllowedTypes is §4.4's parenthetical: "type(object/string/
// integer/number/boolean/array 중)".
var parameterSchemaAllowedTypes = map[string]struct{}{
	"object": {}, "string": {}, "integer": {}, "number": {}, "boolean": {}, "array": {},
}

// parameterSchemaMaxDepth is §4.4's "중첩 깊이 3 초과" cap — depth 3 is the
// last ALLOWED level, so this validator rejects at depth 4. The schema's
// own root object is depth 0; descending into "properties.<key>" or "items"
// increases depth by 1.
const parameterSchemaMaxDepth = 3

// parameterSchemaMaxPatternLen is §4.4's "패턴 길이 상한(256자)".
const parameterSchemaMaxPatternLen = 256

// ValidateParametersSchemaJSON implements 80 §4.4's subset checker. It is
// the sole gate CreateActionType/UpdateActionType run
// :ActionType.parametersSchemaJson through before persisting it (CA1).
//
// Deliberately explicit about what this function does NOT do: it does not
// validate that any candidate parameter VALUE conforms to the schema (that
// is the execution engine's job, stage ③+, per §5.3.3's own note that this
// stage does not implement the engine) — it only validates that the SCHEMA
// TEXT ITSELF stays inside the closed subset that cannot express
// computation (AD7).
func ValidateParametersSchemaJSON(raw string) error {
	if raw == "" {
		return fmt.Errorf("parametersSchemaJson: empty (§4.2 requires at least %s)", `{"type":"object","properties":{}}`)
	}
	var doc any
	dec := json.NewDecoder(strings.NewReader(raw))
	// Reject trailing garbage / multiple top-level values the same way
	// encoding/json's Unmarshal would silently ignore trailing bytes on —
	// belt-and-suspenders for a field whose entire purpose is a closed
	// grammar; a decoder that tolerates trailing tokens is a decoder that
	// tolerates smuggled content after what a casual reviewer would assume
	// is "the schema".
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("parametersSchemaJson: invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("parametersSchemaJson: trailing data after the JSON document")
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return fmt.Errorf("parametersSchemaJson: top level must be a JSON object")
	}
	return validateSchemaNode(root, 0)
}

func validateSchemaNode(node map[string]any, depth int) error {
	if depth > parameterSchemaMaxDepth {
		return fmt.Errorf("parametersSchemaJson: nesting depth exceeds %d (§4.4)", parameterSchemaMaxDepth)
	}

	for key, val := range node {
		if _, allowed := parameterSchemaAllowedKeys[key]; !allowed {
			// Covers §4.4's explicit "금지" list ($ref, $id, $schema, allOf/
			// anyOf/oneOf/not, if/then/else, patternProperties, format,
			// contentSchema) AND any keyword §4.4 never mentions at all —
			// both are "not in the allowed list" (see package doc comment's
			// fail-closed rationale). additionalProperties is handled below
			// as a special case (allowed, but only as a boolean).
			if key == "additionalProperties" {
				if _, isBool := val.(bool); !isBool {
					return fmt.Errorf("parametersSchemaJson: \"additionalProperties\" must be a boolean, not a nested schema (§4.4 forbids additionalProperties:{schema})")
				}
				continue
			}
			return fmt.Errorf("parametersSchemaJson: keyword %q is not in §4.4's allowed subset (rejected, not ignored — fail-closed)", key)
		}

		switch key {
		case "type":
			typeStr, isStr := val.(string)
			if !isStr {
				return fmt.Errorf("parametersSchemaJson: \"type\" must be a string")
			}
			if _, ok := parameterSchemaAllowedTypes[typeStr]; !ok {
				return fmt.Errorf("parametersSchemaJson: \"type\":%q is not one of object/string/integer/number/boolean/array", typeStr)
			}
		case "properties":
			propsMap, isMap := val.(map[string]any)
			if !isMap {
				return fmt.Errorf("parametersSchemaJson: \"properties\" must be an object")
			}
			for propName, propSchemaRaw := range propsMap {
				propSchema, isMap := propSchemaRaw.(map[string]any)
				if !isMap {
					return fmt.Errorf("parametersSchemaJson: properties.%s must be a schema object", propName)
				}
				if err := validateSchemaNode(propSchema, depth+1); err != nil {
					return fmt.Errorf("properties.%s: %w", propName, err)
				}
			}
		case "items":
			itemsSchema, isMap := val.(map[string]any)
			if !isMap {
				return fmt.Errorf("parametersSchemaJson: \"items\" must be a single schema object (no tuple-form array of schemas — that is outside §4.4's subset)")
			}
			if err := validateSchemaNode(itemsSchema, depth+1); err != nil {
				return fmt.Errorf("items: %w", err)
			}
		case "required":
			arr, isArr := val.([]any)
			if !isArr {
				return fmt.Errorf("parametersSchemaJson: \"required\" must be an array")
			}
			for _, e := range arr {
				if _, isStr := e.(string); !isStr {
					return fmt.Errorf("parametersSchemaJson: \"required\" entries must be strings")
				}
			}
		case "enum":
			if _, isArr := val.([]any); !isArr {
				return fmt.Errorf("parametersSchemaJson: \"enum\" must be an array")
			}
		case "minimum", "maximum":
			if !isJSONNumber(val) {
				return fmt.Errorf("parametersSchemaJson: %q must be numeric", key)
			}
		case "minLength", "maxLength", "minItems", "maxItems":
			if !isJSONNumber(val) {
				return fmt.Errorf("parametersSchemaJson: %q must be numeric", key)
			}
		case "description", "default":
			// §4.4 places no shape restriction beyond "description" reading
			// as free text and "default" as any JSON value the surrounding
			// type would accept; no further check is one of this
			// validator's known, accepted gaps (see final report).
		case "pattern":
			patStr, isStr := val.(string)
			if !isStr {
				return fmt.Errorf("parametersSchemaJson: \"pattern\" must be a string")
			}
			if len(patStr) > parameterSchemaMaxPatternLen {
				return fmt.Errorf("parametersSchemaJson: \"pattern\" exceeds %d characters (§4.4)", parameterSchemaMaxPatternLen)
			}
			// RE2 compile check (Go's regexp/syntax — the same engine
			// net/http and this codebase's regexp.MustCompile calls use
			// elsewhere, e.g. actiontype_capabilitykeys.go's
			// capabilityKeyPattern). §4.4's DoS argument rests on RE2's
			// linear-time guarantee; a pattern that does not even compile
			// as RE2 syntax must be rejected outright, not silently stored
			// as a value nothing can ever match against.
			if _, err := syntax.Parse(patStr, syntax.Perl); err != nil {
				return fmt.Errorf("parametersSchemaJson: \"pattern\" does not compile as an RE2 pattern: %w", err)
			}
			// §4.4: "패턴 길이 상한(256자)과 입력 길이 상한(maxLength 필수)을
			// 둔다" — read together, a schema object using "pattern" must
			// also carry "maxLength" in the SAME object, so an attacker
			// cannot pair an expensive-looking pattern with an unbounded
			// input length.
			if _, hasMaxLength := node["maxLength"]; !hasMaxLength {
				return fmt.Errorf("parametersSchemaJson: a schema using \"pattern\" must also declare \"maxLength\" in the same object (§4.4)")
			}
		}
	}
	return nil
}

func isJSONNumber(v any) bool {
	switch v.(type) {
	case json.Number, float64, int, int64:
		return true
	default:
		return false
	}
}
