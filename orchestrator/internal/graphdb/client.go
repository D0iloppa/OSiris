// Package graphdb is osiris-orchestrator's READ-ONLY view of Neo4j.
//
// This does not violate D17/G7 ("Neo4j 쓰기는 core의 graph-write API 단일
// 관문"): that rule is about WRITES. Reads are explicitly expected of the
// orchestrator by design — 10-core-architecture.md §1.4's reconcile
// pseudocode calls `graph.DeriveAgentWorkloads()` directly, and §2.6's
// network placement table puts osiris-orchestrator on osiris-data
// specifically so it can reach Neo4j. Every session opened here uses
// neo4j.AccessModeRead; there is no ExecuteWrite anywhere in this package,
// and no function in it accepts a caller-supplied Cypher string.
package graphdb

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Client struct {
	driver neo4j.DriverWithContext
}

func NewClient(ctx context.Context, uri, user, password string) (*Client, error) {
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""))
	if err != nil {
		return nil, fmt.Errorf("create neo4j driver: %w", err)
	}
	if err := driver.VerifyConnectivity(ctx); err != nil {
		_ = driver.Close(ctx)
		return nil, fmt.Errorf("verify neo4j connectivity: %w", err)
	}
	return &Client{driver: driver}, nil
}

func (c *Client) Close(ctx context.Context) error { return c.driver.Close(ctx) }

func (c *Client) Ping(ctx context.Context) error { return c.driver.VerifyConnectivity(ctx) }

func (c *Client) read(ctx context.Context) neo4j.SessionWithContext {
	return c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
}
