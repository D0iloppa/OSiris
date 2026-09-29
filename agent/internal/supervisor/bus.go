package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"osiris-agent/internal/health"
)

// Bus owns the NATS connection and the durable-publish outbox.
//
// It has a mutex. That is allowed and safe BECAUSE the health handlers never
// touch this type: the only thing the probes learn about NATS is a single
// atomic bool/string that this file pushes into health.State from nats.go's
// own connection callbacks (SetNATSStatus). If a future edit makes a probe
// call into Bus, the §5.4 invariant breaks — hence this comment sits on the
// mutex itself rather than in a design doc.
type Bus struct {
	employeeID string
	hs         *health.State

	mu   sync.Mutex
	nc   *nats.Conn
	js   nats.JetStreamContext
	seq  uint64
	open bool
}

func NewBus(employeeID string, hs *health.State) *Bus {
	return &Bus{employeeID: employeeID, hs: hs}
}

// Connect (re)establishes the connection with the supplied credentials.
// Called at boot and again whenever loadCreds reports a changed fingerprint —
// §8.4: "NATS 자격증명 ... 30일. 회전 시 supervisor 재연결(무중단)". The old
// connection is drained rather than closed abruptly so an in-flight PubAck is
// not lost.
func (b *Bus) Connect(url string, c Creds) error {
	opts := []nats.Option{
		nats.Name("osiris-agent-" + b.employeeID),
		nats.UserInfo(c.NATSUser, c.NATSPassword),
		nats.MaxReconnects(-1), // never give up: RECONNECTING is a live state per §5.4
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectJitter(500*time.Millisecond, 2*time.Second),
		nats.PingInterval(20 * time.Second),
		nats.MaxPingsOutstanding(3),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			// DISCONNECTED is NOT closed — do not mark it fatal for liveness.
			b.hs.SetNATSStatus(statusName(nc))
			log.Printf("supervisor: nats disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			b.hs.SetNATSStatus(statusName(nc))
			log.Printf("supervisor: nats reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			b.hs.SetNATSStatus("CLOSED")
			log.Printf("supervisor: nats connection CLOSED")
		}),
		nats.ErrorHandler(func(_ *nats.Conn, s *nats.Subscription, err error) {
			subj := ""
			if s != nil {
				subj = s.Subject
			}
			log.Printf("supervisor: nats async error (subject=%q): %v", subj, err)
		}),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		b.hs.SetNATSStatus("CONNECT_FAILED")
		return fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		nc.Close()
		return fmt.Errorf("jetstream context: %w", err)
	}

	b.mu.Lock()
	old := b.nc
	b.nc, b.js, b.open = nc, js, true
	b.mu.Unlock()

	if old != nil {
		// Drain (not Close) so queued PubAcks finish; ClosedHandler on the
		// OLD connection would otherwise flip natsClosed=true and fail
		// liveness for a rotation that is supposed to be seamless. The old
		// connection's handlers are replaced before we drain, so the only
		// remaining risk is its ClosedHandler firing — which is why the
		// handler above re-reads the CURRENT connection's status instead of
		// unconditionally trusting the callback.
		go func() {
			_ = old.Drain()
			b.hs.SetNATSStatus(b.Status())
		}()
	}
	b.hs.SetNATSStatus(statusName(nc))
	return nil
}

func statusName(nc *nats.Conn) string {
	if nc == nil {
		return "CLOSED"
	}
	switch nc.Status() {
	case nats.CONNECTED:
		return "CONNECTED"
	case nats.CONNECTING:
		return "CONNECTING"
	case nats.RECONNECTING:
		return "RECONNECTING"
	case nats.DRAINING_PUBS, nats.DRAINING_SUBS:
		return "DRAINING"
	case nats.CLOSED:
		return "CLOSED"
	default:
		return "DISCONNECTED"
	}
}

// Status returns the CURRENT connection's state name. Used by the connection
// callbacks and the heartbeat; never by a health handler (the handler reads
// the atomic this pushes into health.State).
func (b *Bus) Status() string {
	b.mu.Lock()
	nc := b.nc
	b.mu.Unlock()
	return statusName(nc)
}

func (b *Bus) conn() (*nats.Conn, nats.JetStreamContext) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nc, b.js
}

// Subscribe wraps the raw connection so callers do not have to reach for it.
func (b *Bus) Subscribe(subject string, h func(*nats.Msg)) (*nats.Subscription, error) {
	nc, _ := b.conn()
	if nc == nil {
		return nil, errors.New("not connected")
	}
	return nc.Subscribe(subject, h)
}

// PullSubscribe BINDS to a durable consumer the orchestrator already created
// (§5.3's Provisioning step: "볼륨/자격증명/설정/NATS사용자 생성"). The agent
// deliberately does NOT create its own consumer: creating one requires
// $JS.API.CONSUMER.CREATE rights on the INBOX stream, and an agent that can
// create consumers on INBOX can create one filtered on ANOTHER TEAM'S inbox
// subject and read it. D14 makes cross-team *publish* a server-enforced
// impossibility; letting agents create arbitrary consumers would leave the
// read direction wide open. Bind-only keeps the agent's JetStream rights down
// to "next message on MY consumer" + "ack".
func (b *Bus) PullSubscribe(subject, stream, durable string) (*nats.Subscription, error) {
	_, js := b.conn()
	if js == nil {
		return nil, errors.New("not connected")
	}
	return js.PullSubscribe(subject, durable, nats.Bind(stream, durable))
}

// --- publishing -----------------------------------------------------------

// PublishEphemeral is for Core NATS subjects with no stream behind them:
// heartbeat.agent.<id> and ops.log.<id> (§7.2: "(스트림 없음) heartbeat.*,
// ops.log.* — 10초 하트비트를 영구 저장할 이유가 없다"). A failure here is
// DROPPED, never spooled: spooling ephemeral high-volume data would fill the
// workspace volume with logs nobody will read, and the outbox exists to
// protect durable results, not chatter.
func (b *Bus) PublishEphemeral(subject string, payload any) {
	nc, _ := b.conn()
	if nc == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = nc.Publish(subject, data)
}

// PublishDurable publishes to a JetStream-backed subject with an explicit
// Nats-Msg-Id, and SPOOLS to the outbox on any failure.
//
// §5.5: "각 메시지는 Nats-Msg-Id(= taskId:attempt:eventType)를 가지므로
// JetStream 중복 제거 창(2분)과 core의 멱등 처리로 흡수된다." The msgID is
// therefore not an optimization — it is the thing that makes at-least-once
// redelivery (and outbox replay after a reconnect) safe.
func (b *Bus) PublishDurable(subject, msgID string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("supervisor: marshal payload for %s: %v", subject, err)
		return
	}
	if err := b.publishDurableRaw(subject, msgID, data); err != nil {
		log.Printf("supervisor: publish %s failed (%v) — spooling to outbox", subject, err)
		if serr := b.spool(subject, msgID, data); serr != nil {
			log.Printf("supervisor: CRITICAL: spool %s failed: %v", subject, serr)
		}
	}
}

func (b *Bus) publishDurableRaw(subject, msgID string, data []byte) error {
	_, js := b.conn()
	if js == nil {
		return errors.New("not connected")
	}
	opts := []nats.PubOpt{nats.AckWait(10 * time.Second)}
	if msgID != "" {
		opts = append(opts, nats.MsgId(msgID))
	}
	_, err := js.Publish(subject, data, opts...)
	return err
}

type spooledMsg struct {
	Subject string          `json:"subject"`
	MsgID   string          `json:"msgId"`
	Payload json.RawMessage `json:"payload"`
	SpoolAt time.Time       `json:"spoolAt"`
}

// spool writes one undeliverable durable message to
// /workspace/.osiris/outbox/<seq>.json (§5.5: "결과 발행 실패는
// /workspace/.osiris/outbox/<seq>.json에 스풀하고 재연결 시 순서대로
// 재전송"). The sequence number is monotonic within a process and the
// filename is zero-padded so lexical order == send order, which is what
// makes "순서대로" true across a restart without any index file.
func (b *Bus) spool(subject, msgID string, data []byte) error {
	if err := os.MkdirAll(OutboxDir, 0o755); err != nil {
		return err
	}
	b.mu.Lock()
	b.seq++
	seq := b.seq
	b.mu.Unlock()

	name := fmt.Sprintf("%013d-%06d.json", time.Now().UnixMilli(), seq)
	rec, err := json.Marshal(spooledMsg{Subject: subject, MsgID: msgID, Payload: data, SpoolAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	tmp := filepath.Join(OutboxDir, name+".tmp")
	if err := os.WriteFile(tmp, rec, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(OutboxDir, name))
}

// FlushOutbox replays spooled messages in filename (= chronological) order,
// deleting each one only after a successful PubAck. §5.2's outboxFlusher
// goroutine calls this on a ticker; Drain calls it once more before exit
// (§5.5's "outbox 플러시" step, which appears in all three shutdown paths).
func (b *Bus) FlushOutbox() (sent int, remaining int) {
	entries, err := os.ReadDir(OutboxDir)
	if err != nil {
		return 0, 0
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		p := filepath.Join(OutboxDir, n)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m spooledMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			// Unparseable spool file: remove it rather than retrying forever.
			_ = os.Remove(p)
			continue
		}
		if err := b.publishDurableRaw(m.Subject, m.MsgID, m.Payload); err != nil {
			return sent, len(names) - sent
		}
		_ = os.Remove(p)
		sent++
	}
	return sent, 0
}

// Drain closes the connection gracefully. Safe to call twice.
func (b *Bus) Drain(ctx context.Context) {
	b.mu.Lock()
	nc := b.nc
	b.open = false
	b.mu.Unlock()
	if nc == nil {
		return
	}
	done := make(chan struct{})
	go func() { _ = nc.Drain(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		nc.Close()
	}
}

// MsgID builds §5.5's "taskId:attempt:eventType" identity.
func MsgID(taskID string, attempt int, eventType string) string {
	return taskID + ":" + strconv.Itoa(attempt) + ":" + eventType
}
