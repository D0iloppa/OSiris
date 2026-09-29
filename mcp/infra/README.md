# mcp\_\_infra — infra topology read + proposal tools

MCP server over osiris-api's infra topology surface
(`docs/design/90-infra-topology.md` §7.1, §7.2 B단계, §11 C-1). It does
**not** read k8s directly and does **not** apply/scale/delete anything
directly — every tool calls `osiris-api`'s `/_api/infra/*` routes with a
machine principal's `osm_…` Bearer token, so there remains a single
read/propose surface/projection shared by the console canvas and any agent
using this MCP server.

## Tools

| tool | calls | notes |
|---|---|---|
| `infra_topology` | `GET /_api/infra/topology` | node → namespace → workload → pod hierarchy |
| `infra_nodes` | `GET /_api/infra/nodes` | |
| `infra_namespaces` | `GET /_api/infra/namespaces` | |
| `infra_pods` | `GET /_api/infra/pods` | optional `namespace` arg filters client-side (osiris-api has no server-side filter for this route) |
| `infra_metrics` | `GET /_api/infra/metrics` | best-effort; `available:false` in the body is not a tool error |
| `infra_propose_apply` | `POST /_api/infra/proposals` (`actionTypeId:"infra.apply"`) | **proposal only** — see below |
| `infra_propose_scale` | `POST /_api/infra/proposals` (`actionTypeId:"infra.scale"`) | **proposal only** — see below |
| `infra_propose_delete` | `POST /_api/infra/proposals` (`actionTypeId:"infra.delete"`) | **proposal only** — see below |

### Write tools create a proposal — nothing else

`docs/design/90-infra-topology.md` §11 (IN17/IN18/IN19, "C-1 — 제안→승인
척추"). The three propose tools each create ONE pending, admin-approval-gated
`:Approval` node via `osiris-api`'s `POST /_api/infra/proposals` — the single
route IN18 deliberately allows a machine principal to reach. Creating a
proposal is **inert**: it changes nothing in the cluster and grants no one
any new capability, because nothing runs until a human administrator decides
it in the console (or the equivalent REST decide route). Even once approved,
**this build does not execute it** — turning an approved proposal into a real
cluster change is C-2, a later stage this package's code does not implement.

**There is no decide/apply tool in this server, and there never will be one
authenticated the way this server is.** IN19 restricts deciding a proposal
(`POST /_api/infra/proposals/{id}/decision`) to a session-authenticated
*person*, and `osiris-api`'s router refuses a machine principal there
(`PersonOnly`) regardless of what this code does — a machine-principal bearer
token is structurally the wrong credential for that route. Do not add a
decide/apply tool to this package.

## Configuration

| env var | required | meaning |
|---|---|---|
| `OSIRIS_BASE_URL` | yes | osiris-api origin reachable from wherever this MCP server runs, e.g. `https://osiris.example.com` (no trailing slash) |
| `OSIRIS_INFRA_TOKEN` | yes | an `osm_…` machine principal token issued with the `org.read.ops` permission — reused for the propose tools too (IN18: proposal creation needs no capability-shaped scope beyond this, since a proposal is inert; see `api/internal/api/server.go`'s route comment for the full argument) |

If either is missing, every tool call fails immediately with a clear error —
there is no silent fallback.

### Getting a token

Issuing a machine principal requires an authenticated **person** session
(`POST /api/machine-principals` is `PersonOnly`) — this cannot be automated
from this MCP server or from an agent. Run
`../../bin/issue-infra-mcp-token.sh` (from a shell that already has an
`org.admin` person session, e.g. logged into the osiris console / holding the
session cookie) to see the exact `curl` invocation, or run it directly if you
have `OSIRIS_SESSION_COOKIE` set. See that script for details. The token is
shown exactly once at issuance time — store it in a secret store, not in
shell history or a file in this repo.

## Registering with Claude Code

```bash
claude mcp add infra \
  --scope user \
  -- node <repo-root>/mcp/infra/src/index.js
```

Then set the env vars, either via `claude mcp add --env` flags:

```bash
claude mcp add infra \
  --scope user \
  --env OSIRIS_BASE_URL=https://osiris.example.com \
  --env OSIRIS_INFRA_TOKEN=osm_xxxxxxxx \
  -- node <repo-root>/mcp/infra/src/index.js
```

or via an equivalent entry in `.mcp.json` / `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "infra": {
      "command": "node",
      "args": ["<repo-root>/mcp/infra/src/index.js"],
      "env": {
        "OSIRIS_BASE_URL": "https://osiris.example.com",
        "OSIRIS_INFRA_TOKEN": "osm_xxxxxxxx"
      }
    }
  }
}
```

## Local dev

```bash
cd <repo-root>/mcp/infra
npm install
OSIRIS_BASE_URL=https://osiris.example.com OSIRIS_INFRA_TOKEN=osm_xxx node src/index.js
```

It speaks MCP stdio JSON-RPC; there is nothing to "run and see" beyond that —
drive it through an MCP client (Claude Code, `npx @modelcontextprotocol/inspector`, etc).
