#!/usr/bin/env node
// mcp__infra__* — read-only MCP tools over osiris-api's infra topology
// surface (docs/design/90-infra-topology.md §7.1, §7.2 B단계).
//
// This server does NOT read k8s directly. It calls osiris-api's
// `/_api/infra/*` routes (the same read surface the console canvas uses)
// with a machine principal's `osm_…` Bearer token, keeping a single read
// surface/projection per §7.1 ("osiris-api read 표면을 그대로 감싼다").
//
// [2026-09-11, C-1] Write tools now exist too — infra_propose_apply/_scale/
// _delete (docs/design/90-infra-topology.md §11 IN17/IN18). Each ONLY
// creates a pending :Approval proposal via osiris-api's
// POST /_api/infra/proposals (an :ActionType{gateClass:"approval.admin"}
// row's approval, per §7.1's original instruction, quoted below for
// history). None of them call k8s directly, none of them apply/scale/delete
// anything, and NONE OF THEM MAY EVER CALL THE DECISION ROUTE
// (POST /_api/infra/proposals/{id}/decision) — IN19 restricts deciding to a
// session-authenticated person via the console/REST path, and this server's
// machine-principal bearer token is structurally the wrong credential for
// that route regardless of what code runs here. See postInfra's doc comment
// for the enforcement detail.
//
// Original B단계 note, still true of the five read tools above: "per §7.1 a
// write tool must create an :ActionType proposal that goes through the
// approval.admin gate, never call osiris-api or k8s directly" — that is
// exactly what the three propose tools below do.
//
// Config (env):
//   OSIRIS_BASE_URL     e.g. https://osiris.example.com  (no trailing slash)
//   OSIRIS_INFRA_TOKEN  an `osm_…` machine principal token, issued with
//                       permission "org.read.ops" via
//                       POST /api/machine-principals (PersonOnly — a human
//                       must issue it; see ../../bin/issue-infra-mcp-token.sh
//                       for the exact procedure).
//
// See ../README.md for registration instructions.

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

const BASE_URL = (process.env.OSIRIS_BASE_URL || "").replace(/\/+$/, "");
const TOKEN = process.env.OSIRIS_INFRA_TOKEN || "";

function configError() {
  const missing = [];
  if (!BASE_URL) missing.push("OSIRIS_BASE_URL");
  if (!TOKEN) missing.push("OSIRIS_INFRA_TOKEN");
  if (missing.length === 0) return null;
  return (
    `mcp__infra__*: missing required env var(s): ${missing.join(", ")}. ` +
    "OSIRIS_BASE_URL is the osiris-api origin (e.g. https://osiris.example.com). " +
    "OSIRIS_INFRA_TOKEN is an osm_… machine principal token with org.read.ops " +
    "permission, issued by a person session via POST /api/machine-principals " +
    "— see bin/issue-infra-mcp-token.sh at the OSiris repo root for the exact " +
    "procedure. This server refuses to guess or fall back silently."
  );
}

/**
 * Call one GET /_api/infra/<path> route on osiris-api with the machine
 * principal bearer. Throws on missing config, non-2xx, or network failure —
 * the tool handler turns that into an MCP error result so the caller sees a
 * clear reason rather than a bare stack trace.
 */
async function getInfra(path) {
  const cfgErr = configError();
  if (cfgErr) throw new Error(cfgErr);

  const url = `${BASE_URL}/_api/infra/${path}`;
  let res;
  try {
    res = await fetch(url, {
      method: "GET",
      headers: {
        Authorization: `Bearer ${TOKEN}`,
        Accept: "application/json",
      },
    });
  } catch (err) {
    throw new Error(`request to ${url} failed: ${err.message}`);
  }

  const text = await res.text();
  if (!res.ok) {
    throw new Error(
      `GET ${url} -> ${res.status} ${res.statusText}: ${text.slice(0, 500)}`
    );
  }
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`GET ${url} returned non-JSON body: ${text.slice(0, 500)}`);
  }
}

/**
 * [2026-09-11, C-1] POST one write route on osiris-api's infra proposal
 * surface (docs/design/90-infra-topology.md §11 IN17/IN18). The ONLY route
 * this file ever posts to is /_api/infra/proposals — creating a proposal.
 * There is no function in this file, and must never be one, that posts to
 * /_api/infra/proposals/{id}/decision: IN19 restricts deciding to a
 * session-authenticated PERSON via the console/REST path, and a machine
 * principal's bearer token (what this whole server authenticates with) is
 * structurally the wrong kind of credential for that route regardless —
 * osiris-api's router refuses it with PersonOnly before this server's
 * request would even reach a handler. See this file's top-of-file doc
 * comment and the README for the same point stated for a human reader.
 */
async function postInfra(path, body) {
  const cfgErr = configError();
  if (cfgErr) throw new Error(cfgErr);

  const url = `${BASE_URL}/_api/infra/${path}`;
  let res;
  try {
    res = await fetch(url, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${TOKEN}`,
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });
  } catch (err) {
    throw new Error(`request to ${url} failed: ${err.message}`);
  }

  const text = await res.text();
  if (!res.ok) {
    throw new Error(
      `POST ${url} -> ${res.status} ${res.statusText}: ${text.slice(0, 500)}`
    );
  }
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`POST ${url} returned non-JSON body: ${text.slice(0, 500)}`);
  }
}

/**
 * proposeInputSchema is shared by all three propose tools below — same
 * target shape core/internal/graphdb/actiontype_infra.go's
 * parametersSchemaJson declares for infra.apply/scale/delete (namespace,
 * kind, name, optional manifestJson).
 */
const proposeInputSchema = {
  type: "object",
  properties: {
    namespace: { type: "string", description: "Target Kubernetes namespace." },
    kind: { type: "string", description: "Target resource kind, e.g. Deployment." },
    name: { type: "string", description: "Target resource name." },
    manifestJson: {
      type: "string",
      description:
        "Optional opaque proposed manifest/spec, as a JSON string. Stored " +
        "verbatim by osiris-core — NOT parsed, validated against a live " +
        "cluster, or applied by this build (C-1 excludes execution).",
    },
  },
  required: ["namespace", "kind", "name"],
  additionalProperties: false,
};

/**
 * proposeHandler builds the shared POST body for one infra :ActionType id —
 * the only thing that differs between the three propose tools below.
 */
function proposeHandler(actionTypeId) {
  return async (args) => {
    const { namespace, kind, name, manifestJson } = args || {};
    if (!namespace || !kind || !name) {
      throw new Error("namespace, kind and name are all required");
    }
    return postInfra("proposals", {
      actionTypeId,
      namespace,
      kind,
      name,
      ...(manifestJson ? { manifestJson } : {}),
    });
  };
}

const TOOLS = [
  {
    name: "infra_topology",
    description:
      "Read the cluster topology (nodes → namespaces → workloads → pods hierarchy) via osiris-api GET /_api/infra/topology. Read-only.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
    handler: async () => getInfra("topology"),
  },
  {
    name: "infra_nodes",
    description:
      "List cluster nodes via osiris-api GET /_api/infra/nodes. Read-only.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
    handler: async () => getInfra("nodes"),
  },
  {
    name: "infra_namespaces",
    description:
      "List namespaces via osiris-api GET /_api/infra/namespaces. Read-only.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
    handler: async () => getInfra("namespaces"),
  },
  {
    name: "infra_pods",
    description:
      "List pods (status summary projection) via osiris-api GET /_api/infra/pods. " +
      "Optional `namespace` argument filters the result client-side (the " +
      "upstream route itself returns all pods; osiris-api does not offer a " +
      "server-side namespace filter today). Read-only.",
    inputSchema: {
      type: "object",
      properties: {
        namespace: {
          type: "string",
          description: "If set, only pods in this namespace are returned.",
        },
      },
      additionalProperties: false,
    },
    handler: async (args) => {
      const data = await getInfra("pods");
      const ns = args && typeof args.namespace === "string" ? args.namespace.trim() : "";
      if (!ns) return data;
      const pods = Array.isArray(data?.pods) ? data.pods : [];
      return { pods: pods.filter((p) => p?.namespace === ns) };
    },
  },
  {
    name: "infra_metrics",
    description:
      "Read node/pod resource metrics via osiris-api GET /_api/infra/metrics. " +
      "Best-effort: the response may report `available: false` if metrics-server " +
      "is briefly unreachable — that is not a tool error. Read-only.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
    handler: async () => getInfra("metrics"),
  },

  // ── [2026-09-11, C-1] Write tools — PROPOSAL CREATION ONLY ─────────────
  //
  // docs/design/90-infra-topology.md §11 IN17/IN18. Each of these three
  // tools calls osiris-api's POST /_api/infra/proposals — the ONLY infra
  // write route a machine principal may ever reach (IN18's one deliberate
  // HI15-style exception, enforced at osiris-api's router, not here). The
  // proposal this creates is INERT: it changes nothing about the cluster
  // and grants no one any new capability. It only creates a pending
  // :Approval a human administrator must later approve or reject through
  // the console (or the equivalent REST decide route), which this server
  // cannot call — see postInfra's doc comment.
  {
    name: "infra_propose_apply",
    description:
      "Propose applying a Kubernetes manifest (creates a pending admin-approval " +
      ":Approval via osiris-api POST /_api/infra/proposals with " +
      "actionTypeId=infra.apply). Does NOT apply anything — the proposal is " +
      "inert until a human administrator approves it in the console, and even " +
      "then this build does not execute it (C-2, not implemented here).",
    inputSchema: proposeInputSchema,
    handler: proposeHandler("infra.apply"),
  },
  {
    name: "infra_propose_scale",
    description:
      "Propose scaling an infrastructure workload (creates a pending " +
      "admin-approval :Approval via osiris-api POST /_api/infra/proposals with " +
      "actionTypeId=infra.scale). Does NOT scale anything — see infra_propose_apply's " +
      "description for the same inert-until-approved, not-yet-executed caveat.",
    inputSchema: proposeInputSchema,
    handler: proposeHandler("infra.scale"),
  },
  {
    name: "infra_propose_delete",
    description:
      "Propose deleting a Kubernetes resource (creates a pending admin-approval " +
      ":Approval via osiris-api POST /_api/infra/proposals with " +
      "actionTypeId=infra.delete). Does NOT delete anything — see " +
      "infra_propose_apply's description for the same inert-until-approved, " +
      "not-yet-executed caveat.",
    inputSchema: proposeInputSchema,
    handler: proposeHandler("infra.delete"),
  },
];

const server = new Server(
  { name: "mcp__infra", version: "0.1.0" },
  { capabilities: { tools: {} } }
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: TOOLS.map(({ name, description, inputSchema }) => ({
    name,
    description,
    inputSchema,
  })),
}));

server.setRequestHandler(CallToolRequestSchema, async (req) => {
  const tool = TOOLS.find((t) => t.name === req.params.name);
  if (!tool) {
    return {
      isError: true,
      content: [{ type: "text", text: `unknown tool: ${req.params.name}` }],
    };
  }
  try {
    const result = await tool.handler(req.params.arguments || {});
    return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
  } catch (err) {
    return {
      isError: true,
      content: [{ type: "text", text: err instanceof Error ? err.message : String(err) }],
    };
  }
});

const transport = new StdioServerTransport();
await server.connect(transport);
