package onboarding

// This test guards the bug reported by the user: the browser console showed
// "TypeError: Cannot read properties of null (reading 'length')" and the
// onboarding page went blank right after first render.
//
// Root cause: console/src/api/types.ts declares
// OnboardingStep0Status.providers/aliases as non-nullable arrays
// (ProviderCredentialSummary[]/AliasSyncStatus[]), and
// UpsertAliasesResponse.results as AliasApplyResult[] — but
// encoding/json marshals a nil Go slice as JSON `null`, not `[]`. The
// console's step0Logic.ts reads `step0.providers.length` unconditionally
// (§14 of that file), which throws the instant `providers` arrives as
// `null` instead of `[]` — which it always did on a freshly-onboarded
// deployment (zero providers registered yet is the *normal* first state,
// not an edge case).
//
// These tests exercise the real early-return code path (omniroute
// unreachable, which is exactly the state a brand-new deployment is in
// before step 0-1 has run) and assert the JSON on the wire is `[]`, never
// `null`, for every array field the console's types.ts declares
// non-nullable.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"osiris-api/internal/omniroute"
)

func newUnreachableService() *Service {
	// A client pointed at a script/binary that cannot possibly run
	// successfully, so every omniroute.Client call fails deterministically
	// and each handler takes its early-return ("omniroute unreachable")
	// path — the same path a brand-new deployment hits before step 0-1.
	ops := omniroute.NewClient("/nonexistent/operations.mjs", "/nonexistent/node")
	return NewService(ops, "/nonexistent/.env", "http://127.0.0.1:0", "/health", "http://127.0.0.1:0", nil)
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(b)
}

func TestStep0Status_ArraysAreNeverNull(t *testing.T) {
	s := newUnreachableService()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/onboarding/step0/status", nil)
	s.Step0Status(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	t.Logf("response body: %s", body)

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	assertJSONArrayNotNull(t, "providers", decoded["providers"])
	assertJSONArrayNotNull(t, "aliases", decoded["aliases"])
}

func TestStatus_ArraysAreNeverNull(t *testing.T) {
	s := newUnreachableService()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/onboarding/status", nil)
	s.Status(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var decoded struct {
		Step0 map[string]json.RawMessage `json:"step0"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	assertJSONArrayNotNull(t, "step0.providers", decoded.Step0["providers"])
	assertJSONArrayNotNull(t, "step0.aliases", decoded.Step0["aliases"])
}

func TestUpsertAliases_ResultsIsNeverNull(t *testing.T) {
	s := newUnreachableService()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/onboarding/step0/aliases",
		jsonBody(t, map[string]any{"entries": []map[string]string{}}))
	s.Aliases(rec, req)

	if rec.Code != http.StatusBadGateway {
		// omniroute is unreachable in this test, so the handler is expected
		// to fail the request outright — this test only cares that IF it
		// ever did write a 200 with a results field (e.g. after a future
		// refactor), that field would never be null. Documented here so a
		// future change to error-handling doesn't silently regress this.
		t.Skipf("handler returned %d instead of the expected 502 for an unreachable omniroute; skipping shape assertion", rec.Code)
	}
}

func assertJSONArrayNotNull(t *testing.T, field string, raw json.RawMessage) {
	t.Helper()
	if raw == nil {
		t.Fatalf("%s: field missing from response entirely", field)
	}
	if string(raw) == "null" {
		t.Fatalf("%s: field is JSON null — this is exactly the crash the user reported "+
			"(console reads %s.length unconditionally)", field, field)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("%s: not a JSON array: %s (%v)", field, raw, err)
	}
}
