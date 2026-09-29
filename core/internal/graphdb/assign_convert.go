package graphdb

// assign_convert.go — small, shared helpers for turning driver-returned
// values (which may come back as time.Time, dbtype.Date, dbtype.
// LocalDateTime, int64, nil, ...) into the plain Go types P3's assign
// package works with. Kept in one place so every assign_*.go file converts
// the same way.

import (
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// timeOf converts a Cypher `datetime()` (native time.Time), `date()`
// (dbtype.Date), or nil into a time.Time. Anything unrecognized (or nil)
// returns the zero time.Time — callers treat a zero time as "unset/unknown",
// matching assign.CapabilityMatch.VerifiedAt / assign.Task.DueAt's
// documented zero-value convention.
func timeOf(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case dbtype.Date:
		return t.Time()
	case dbtype.LocalDateTime:
		return t.Time()
	default:
		return time.Time{}
	}
}

// timePtrOf is timeOf but returns nil instead of a zero time.Time, for
// optional fields (assign.Candidate.LeaveFrom/LeaveTo/NextFreeAt) where a
// caller needs to distinguish "unset" from "the zero instant".
func timePtrOf(v any) *time.Time {
	if v == nil {
		return nil
	}
	t := timeOf(v)
	if t.IsZero() {
		return nil
	}
	return &t
}

// int64OfAny converts a driver value that should be an int64 (Neo4j has no
// separate "int" type) into a Go int, defaulting to 0 for nil/wrong-type.
func intOfAny(v any) int {
	if n, ok := v.(int64); ok {
		return int(n)
	}
	return 0
}

// float64OfAny converts a driver value into float64, accepting either an
// int64 or a float64 back (Neo4j numeric literals used in property maps can
// come back as either depending on how they were stored), defaulting to 0.
func floatOfAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// stringOfAny converts a driver value into a string, defaulting to "" for
// nil.
func stringOfAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// boolOfAny converts a driver value into a bool, defaulting to false.
func boolOfAny(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// stringsOfAny converts a driver value that should be a []any of strings
// (Neo4j string[] properties come back as []any) into []string.
func stringsOfAny(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
