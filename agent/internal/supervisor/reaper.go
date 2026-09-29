package supervisor

import (
	"log"
	"sync/atomic"
	"syscall"
	"time"

	"osiris-agent/internal/health"
)

// reaper is the "childReaper goroutine" §5.4 lists as a liveness input.
//
// It has two jobs, and the SECOND one is the one the design cares about:
//
//  1. As PID 1 the supervisor inherits orphaned descendants (a Bash tool call
//     that backgrounded something, an MCP server whose parent died). Nobody
//     else will wait(2) for them, so without this they accumulate as zombies
//     until the PID table is exhausted (§5.1's reason 2 for supervisor-as-PID-1:
//     "프로세스 트리 관리(좀비 수거, 프로세스 그룹 시그널)가 표준 POSIX로
//     끝난다").
//  2. It stamps its own liveness tick. §5.4: "childReaper goroutine 생존
//     (자체 tick으로 확인)". If this goroutine dies or wedges, /livez goes 503
//     within 30s — because a PID 1 that has stopped reaping is a container
//     that will fail later in a much more confusing way.
//
// RACE NOTE (deliberate design choice): os/exec's Cmd.Wait() calls wait4 on
// its own pid. If this reaper called wait4(-1) while a managed child was
// running, it could consume that child's exit status first and make Cmd.Wait()
// return ECHILD — i.e. the supervisor would lose the child's exit code, which
// is exactly the information runTask needs to decide success/failure and
// whether to clear the checkpoint. So the reaper only sweeps while NO managed
// child exists (managedChildPID == 0). Orphans that appear during a turn are
// collected as soon as the turn ends. The cost is a bounded delay in reaping;
// the alternative cost would be losing turn outcomes, which is unacceptable.
func reaper(hs *health.State, managedChildPID *atomic.Int32, stop <-chan struct{}) {
	t := time.NewTicker(reaperTickInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			hs.TouchReaper()
			if managedChildPID.Load() != 0 {
				continue // see RACE NOTE above
			}
			for {
				var ws syscall.WaitStatus
				pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
				if err != nil || pid <= 0 {
					break
				}
				log.Printf("supervisor: reaped orphan pid=%d status=%d", pid, ws.ExitStatus())
			}
		}
	}
}
