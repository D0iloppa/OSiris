// Package graphdb is osiris-core's single point of contact with Neo4j.
//
// Nothing outside this package (and its sibling api/writeops callers) talks
// to the driver directly — this is the code-level enforcement of A5/G7
// ("Neo4j 쓰기는 core를 통해서만", "여러 컴포넌트가 직접 쓰기 금지"). Within
// osiris-core itself there is no handler that accepts and runs arbitrary
// Cypher from a request body; every write here is a named Go function with a
// fixed, parameterized query.
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

func (c *Client) Close(ctx context.Context) error {
	return c.driver.Close(ctx)
}

// Ping is used by /readyz — it must stay light. VerifyConnectivity reuses the
// pool's routing table and does not take any graph-level lock.
func (c *Client) Ping(ctx context.Context) error {
	return c.driver.VerifyConnectivity(ctx)
}

func (c *Client) Driver() neo4j.DriverWithContext {
	return c.driver
}

func (c *Client) NewSession(ctx context.Context) neo4j.SessionWithContext {
	return c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
}

func (c *Client) NewReadSession(ctx context.Context) neo4j.SessionWithContext {
	return c.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
}
