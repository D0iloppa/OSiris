// Package mcpserver is the stdio MCP server side of §8.2's approval gate.
//
// WHY THIS EXISTS AT ALL (and why it is the design's own choice, not an
// invention): §8.2 leaves an explicitly-flagged `[미검증]` open question —
// whether a hooks section in a settings.json passed via --settings is honoured
// under --bare — and pre-commits to a fallback: "(b) MCP 툴 래핑 — git 조작을
// osiris-git MCP 서버로만 노출하고 Bash에서 git을 deny. (b)가 더 견고하므로
// 훅이 안 되면 (b)로 간다."
//
// The question is now settled by the CLI itself: `claude --help` (2.1.220)
// documents `--bare` as "Minimal mode: skip hooks, ...". Hooks are not loaded.
// So fallback (b) is the implemented path, and the grant-catalog derives, per
// gated permission, BOTH the MCP tool that is allowed AND the Bash form that is
// denied (see orchestrator/internal/catalog).
//
// Transport: JSON-RPC 2.0 over stdio, spoken by this same binary launched as
// `osiris-supervisor -mcp-server <name>` from the rendered mcp.json. Keeping it
// in the supervisor binary means the agent image needs no extra runtime, and the
// tool implementation can be nothing more than "ask the local gate and report
// what it said" — which is precisely the boundary §8.2 wants: the agent never
// performs the external-effect action itself.
package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const protocolVersion = "2024-11-05"

// Tool is one exposed MCP tool.
type Tool struct {
	Name         string
	Description  string
	ApprovalType string // commit | message | spend — matches :Permission.gate's approver kind
	InputSchema  map[string]any
}

// Registry maps an MCP server name to its tools. The names here are the ones
// orchestrator/internal/catalog derives from the grant-catalog; keeping the two
// lists in lockstep is what makes "선언 하나 → 네 층위" (D13) true for the MCP
// layer as well.
var Registry = map[string][]Tool{
	"osiris-git": {
		{
			Name:         "request_commit",
			ApprovalType: "commit",
			Description: "Request that osiris-core commit the current workspace changes on your behalf. " +
				"You do NOT have permission to run `git commit` yourself: OSiris performs approved commits " +
				"with its own credential (your git token is read-only). Provide a commit message and a " +
				"summary of the diff; a human approves, and core executes. You will be told the outcome.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message":     map[string]any{"type": "string", "description": "Commit message"},
					"diffSummary": map[string]any{"type": "string", "description": "Human-readable summary of what changed and why"},
					"paths":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Paths to include"},
				},
				"required": []string{"message", "diffSummary"},
			},
		},
		{
			Name:         "request_push",
			ApprovalType: "commit",
			Description: "Request that osiris-core push the approved commits to the remote. Same rule as " +
				"request_commit: you cannot push yourself; core pushes with its own write credential.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"remote": map[string]any{"type": "string"},
					"branch": map[string]any{"type": "string"},
					"reason": map[string]any{"type": "string"},
				},
				"required": []string{"branch", "reason"},
			},
		},
	},
	"osiris-mailer": {
		{
			Name:         "send",
			ApprovalType: "message",
			Description: "Request that an external message (email) be sent. Requires human approval; the " +
				"actual send is performed by the platform, not by you.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":      map[string]any{"type": "string"},
					"subject": map[string]any{"type": "string"},
					"body":    map[string]any{"type": "string"},
				},
				"required": []string{"to", "subject", "body"},
			},
		},
	},
	"osiris-finance": {
		{
			Name:         "request_spend",
			ApprovalType: "spend",
			Description: "Request approval for a spend. Requires human approval with an amount limit; " +
				"no money moves without it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"amount":   map[string]any{"type": "number"},
					"currency": map[string]any{"type": "string"},
					"purpose":  map[string]any{"type": "string"},
					"vendor":   map[string]any{"type": "string"},
				},
				"required": []string{"amount", "currency", "purpose"},
			},
		},
	},
}

// ServerNames returns the registered server names (sorted by the caller if
// order matters).
func ServerNames() []string {
	out := make([]string, 0, len(Registry))
	for k := range Registry {
		out = append(out, k)
	}
	return out
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve runs the stdio JSON-RPC loop for one server name until stdin closes.
func Serve(name string, in io.Reader, out io.Writer) error {
	tools, ok := Registry[name]
	if !ok {
		return fmt.Errorf("unknown MCP server %q (known: %v)", name, ServerNames())
	}
	gateURL := os.Getenv("OSIRIS_GATE_URL")
	if gateURL == "" {
		gateURL = "http://127.0.0.1:8082"
	}
	taskID := os.Getenv("OSIRIS_TASK_ID")

	br := bufio.NewReaderSize(in, 1<<20)
	enc := json.NewEncoder(out)

	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var req rpcRequest
			if jerr := json.Unmarshal(line, &req); jerr != nil {
				_ = enc.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			} else {
				resp, notification := handle(name, tools, gateURL, taskID, req)
				if !notification {
					_ = enc.Encode(resp)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func handle(serverName string, tools []Tool, gateURL, taskID string, req rpcRequest) (rpcResponse, bool) {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": "1"},
		}
		return resp, false

	case "notifications/initialized", "notifications/cancelled":
		return resp, true // notifications get no response

	case "ping":
		resp.Result = map[string]any{}
		return resp, false

	case "tools/list":
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			list = append(list, map[string]any{
				"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema,
			})
		}
		resp.Result = map[string]any{"tools": list}
		return resp, false

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return resp, false
		}
		var tool *Tool
		for i := range tools {
			if tools[i].Name == p.Name {
				tool = &tools[i]
				break
			}
		}
		if tool == nil {
			resp.Error = &rpcError{Code: -32601, Message: "unknown tool " + p.Name}
			return resp, false
		}
		text := callGate(gateURL, taskID, serverName, tool, p.Arguments)
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}
		return resp, false

	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		return resp, false
	}
}

// callGate is the whole tool implementation: hand the request to the local
// supervisor gate and report the human's decision back to the model. Note what
// it does NOT do — it never runs git, never sends mail, never moves money.
// §8.2: "★ 실제 커밋/푸시는 osiris-core가 자기 자격증명으로 수행한다.
// 에이전트에게는 '승인되어 반영됨'만 통보된다."
func callGate(gateURL, taskID, serverName string, tool *Tool, args json.RawMessage) string {
	summary := summarize(args)
	body, _ := json.Marshal(map[string]any{
		"approvalType": tool.ApprovalType,
		"tool":         serverName + "." + tool.Name,
		"taskId":       taskID,
		"summary":      summary,
		"payload":      args,
	})

	// No client timeout: the gate itself bounds the wait at §8.2's 24h, and a
	// shorter timeout here would abandon an approval a human is still looking
	// at. The MCP subprocess blocking is harmless — it is not PID 1 and it is
	// not an input to any health probe.
	client := &http.Client{Timeout: gateMaxClientWait}
	resp, err := client.Post(strings.TrimSuffix(gateURL, "/")+"/gate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "APPROVAL GATE UNREACHABLE: " + err.Error() +
			"\nThe action was NOT performed. Do not attempt to bypass the gate (e.g. by running the shell " +
			"equivalent) — it is denied at a lower layer and the attempt is audited. Report this to your team instead."
	}
	defer func() { _ = resp.Body.Close() }()

	var gr struct {
		ApprovalID string `json:"approvalId"`
		Decision   string `json:"decision"`
		Reason     string `json:"reason"`
		ExecutedBy string `json:"executedBy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "APPROVAL GATE RETURNED AN UNREADABLE RESPONSE: " + err.Error() + " — the action was NOT performed."
	}

	switch gr.Decision {
	case "approved":
		by := gr.ExecutedBy
		if by == "" {
			by = "osiris-core"
		}
		return fmt.Sprintf("APPROVED (approvalId=%s). The action was executed by %s on your behalf. "+
			"Do not repeat it. %s", gr.ApprovalID, by, gr.Reason)
	case "rejected":
		return fmt.Sprintf("REJECTED (approvalId=%s). Reason: %s. Find another approach or return the task.",
			gr.ApprovalID, gr.Reason)
	case "timeout":
		return fmt.Sprintf("NO DECISION (approvalId=%s) within the approval window. The action was NOT performed. "+
			"Stop and report that you are blocked on human approval.", gr.ApprovalID)
	default:
		return fmt.Sprintf("APPROVAL ERROR (approvalId=%s): %s. The action was NOT performed.", gr.ApprovalID, gr.Reason)
	}
}

// gateMaxClientWait is slightly longer than the gate's own 24h bound so the
// gate's answer, not the client's impatience, is always what ends the wait.
const gateMaxClientWait = 25 * time.Hour

func summarize(args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return string(args)
	}
	var parts []string
	for _, k := range []string{"message", "reason", "purpose", "subject", "branch", "to", "amount", "currency", "diffSummary"} {
		if v, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) == 0 {
		return string(args)
	}
	return strings.Join(parts, " ")
}
