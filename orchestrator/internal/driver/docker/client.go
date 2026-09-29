// Package docker is the [1단계] WorkloadDriver implementation
// (docs/design/10-core-architecture.md §6.2, D3) and the ONLY package in
// this repo that talks to the Docker Engine API (G3: "Docker API 타입이
// WorkloadDriver 밖으로 유출되면 안 된다" — enforced here by construction,
// since every exported type from this package that crosses into
// internal/workload is a plain Go value, never a Docker wire type).
//
// This talks to the Engine API directly over the Unix socket with a small
// hand-rolled HTTP client rather than importing github.com/docker/docker's
// SDK (~40 transitive modules for the handful of endpoints this build
// needs: images, volumes, containers, networks). A11 asks for "Docker
// Engine API 직접 호출" — this is that, literally: raw HTTP against the
// documented Engine API, versioned and pinned like everything else in this
// codebase.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// apiVersion is pinned (no floating "latest") per this repo's convention
// (IMPLEMENTATION-NOTES.md #1). v1.43 is comfortably below this host's
// daemon API (1.49, confirmed via `docker version`) and stable for the
// container/volume/network calls this driver makes.
const apiVersion = "v1.43"

type Client struct {
	http *http.Client
}

// NewClient dials the Engine API over the Unix socket mounted at sockPath
// (only ever /var/run/docker.sock, only ever mounted into THIS container —
// G8: agent containers never get this socket).
func NewClient(sockPath string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", sockPath)
		},
	}
	return &Client{http: &http.Client{Transport: tr, Timeout: 60 * time.Second}}
}

func (c *Client) url(path string, query url.Values) string {
	u := "http://docker/" + apiVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// do issues a request and returns the raw status/body. Callers decide how
// to interpret non-2xx (Docker's error bodies are {"message": "..."} JSON).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path, query), reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("docker engine API request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp.StatusCode, out, nil
}

type dockerErr struct {
	Message string `json:"message"`
}

// engineError formats a Docker error body (or the raw bytes if it isn't
// one) into a Go error, tagged with the status code so callers can branch
// on 404 (not found — often a legitimate "doesn't exist yet" case) vs
// anything else (a real failure).
func engineError(status int, body []byte) error {
	var e dockerErr
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return fmt.Errorf("docker engine API status %d: %s", status, e.Message)
	}
	return fmt.Errorf("docker engine API status %d: %s", status, string(body))
}

func ok2xx(status int) bool { return status >= 200 && status < 300 }

// unmarshalStrict decodes a Docker Engine API response body. Not
// DisallowUnknownFields — the Engine API response objects have many more
// fields than the small subset this driver's wire types declare, and that
// is expected (we only decode what we use).
func unmarshalStrict(body []byte, v any) error {
	return json.Unmarshal(body, v)
}
