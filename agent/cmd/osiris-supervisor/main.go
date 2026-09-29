// osiris-supervisor — the OSiris agent container's PID 1
// (docs/design/10-core-architecture.md §5).
//
// This binary has exactly three modes, and the mode is chosen by flag so that
// ONE image serves all of them (§5.7's one-shot job container is "동일 이미지·
// 동일 자격증명·동일 네트워크, 권한 확대 없음"):
//
//	(default)               supervise: main loop, health, drain, resume (§5.2-§5.6)
//	-healthcheck            probe self and exit 0/1 — what Docker HEALTHCHECK runs
//	-one-shot <prompt>      §5.7: run one task, no inbox binding, exit
//	-mcp-server <name>      stdio MCP gate server, launched by claude (§8.2 fallback b)
//
// A4 NOTE, in the opposite direction from osiris-core/osiris-orchestrator:
// THIS is the one image in the system that is SUPPOSED to contain the `claude`
// binary. core and orchestrator must never gain it (they are FROM scratch, so
// they structurally cannot). The asymmetry is the point of A4: "배정 로직은
// LLM이 있는 프로세스에 존재하지 않는다."
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"osiris-agent/internal/mcpserver"
	"osiris-agent/internal/supervisor"
)

func main() {
	var (
		healthcheck = flag.Bool("healthcheck", false, "probe this container's own /livez and exit 0 (healthy) or 1")
		oneShot     = flag.String("one-shot", "", "§5.7 heavy one-shot mode: run this prompt as a single task, then exit")
		oneShotID   = flag.String("one-shot-task-id", "", "taskId to report for -one-shot")
		mcpServer   = flag.String("mcp-server", "", "run as a stdio MCP server (osiris-git|osiris-mailer|osiris-finance)")
	)
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	if *mcpServer != "" {
		// Launched as a child of `claude`, NOT as PID 1. No health server, no
		// NATS connection, no logging to stdout (stdout is the JSON-RPC
		// transport — anything else written there corrupts the protocol).
		log.SetOutput(os.Stderr)
		log.SetFlags(0)
		if err := mcpserver.Serve(*mcpServer, os.Stdin, os.Stdout); err != nil {
			log.Printf("osiris-supervisor -mcp-server %s: %v", *mcpServer, err)
			os.Exit(1)
		}
		return
	}

	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("")

	cfg := supervisor.ConfigFromEnv()
	cfg.OneShotPrompt = *oneShot
	cfg.OneShotTaskID = *oneShotID
	if cfg.EmployeeID == "" {
		log.Printf("osiris-supervisor: FATAL: OSIRIS_EMPLOYEE_ID is not set")
		os.Exit(2)
	}

	sup := supervisor.New(cfg)

	// SIGTERM must reach US FIRST — that is the whole reason the supervisor is
	// PID 1 rather than a sidecar (§5.1 reason 1: "사이드카면 런타임이 두
	// 컨테이너에 동시에 SIGTERM을 보내고 claude가 체크포인트 로직과 무관하게
	// 죽는다 — §5.5가 성립하지 않는다").
	//
	// signal.Notify (not signal.NotifyContext) is used deliberately: cancelling
	// the root context on SIGTERM would cancel the in-flight fetch AND anything
	// context-bound in the turn, whereas §5.5 requires the OPPOSITE for a
	// running child — leave it alone for drain.waitSeconds and let it finish.
	// So the signal drives sup.SignalDrain() and the root context is only
	// cancelled once the drain sequence itself has completed.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for sig := range sigCh {
			log.Printf("osiris-supervisor: received %s — entering drain (§5.5)", sig)
			sup.SignalDrain()
		}
	}()

	if err := sup.Run(ctx); err != nil {
		log.Printf("osiris-supervisor: exiting with error: %v", err)
		os.Exit(1)
	}
	// §5.5: every branch ends in "exit 0". A non-zero exit here would make
	// Docker's restart policy fight the orchestrator's convergence loop (G9).
	os.Exit(0)
}

// runHealthcheck is what Docker's HEALTHCHECK executes. It hits /livez only —
// NOT /readyz. That distinction is load-bearing: Docker health drives
// RESTARTS (via the orchestrator's 3-consecutive-unhealthy enforcement, §5.4),
// and readiness must never cause a restart. An agent whose gateway is down is
// readyz=false and must keep running (it has a checkpoint, a session, and a
// workspace); restarting it would help nothing and lose the in-flight turn.
func runHealthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8081/livez")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: /livez returned %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
