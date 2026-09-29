package graphdb

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
)

// schema.cypher is a byte-for-byte copy of neo4j/init/constraints.cypher
// (docs/design/20-ontology-schema.md §11, "원문 그대로"). osiris-core embeds
// its own copy rather than reading the file at runtime so the binary is
// self-contained (no bind mount needed just for DDL) and so "what schema did
// this build apply" is pinned to the image, not to whatever happens to be on
// disk next to it. See IMPLEMENTATION-NOTES.md for the decision to have core
// itself apply this on every boot (idempotent CREATE ... IF NOT EXISTS)
// rather than relying solely on the external neo4j/apply-schema.sh script —
// that script remains as a belt-and-suspenders bootstrap step, but core no
// longer depends on it having been run.
//
//go:embed schema.cypher
var schemaCypher string

// ApplyMigrations runs the DDL (constraints + indexes) statement by
// statement, each as its own auto-commit query. Neo4j does not allow mixing
// multiple schema-modifying statements inside a single explicit transaction,
// so this deliberately does NOT use a managed transaction the way seed/write
// operations do. This is the one place in osiris-core that runs Cypher
// parsed out of a text blob instead of a fixed parameterized query — and it
// is DDL, not data (§9.1's explicitly stated exception: "스키마 마이그레이션
// — DDL이므로 예외"). It never touches organizational data.
func ApplyMigrations(ctx context.Context, c *Client) error {
	statements := splitStatements(schemaCypher)
	session := c.NewSession(ctx)
	defer session.Close(ctx)

	for _, stmt := range statements {
		if stmt == "" {
			continue
		}
		if _, err := session.Run(ctx, stmt, nil); err != nil {
			return fmt.Errorf("apply schema statement %q: %w", truncate(stmt, 80), err)
		}
	}
	return nil
}

func splitStatements(src string) []string {
	lines := strings.Split(src, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		kept = append(kept, line)
	}
	joined := strings.Join(kept, "\n")
	raw := strings.Split(joined, ";")
	var out []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
