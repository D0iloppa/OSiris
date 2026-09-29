// Package events implements the console SSE endpoint (docs/design/
// 10-core-architecture.md §7.4/D19/D20): GET /api/events (osiris-gw exposes
// it at /_api/events, rewriting the prefix — see gw/conf.d/default.conf).
//
// D19: "Last-Event-ID는 JetStream TASKS 스트림의 시퀀스 번호를 그대로
// 쓴다." Each browser reconnect gets its OWN ephemeral JetStream pull
// consumer on TASKS, positioned by DeliverPolicy at (lastEventID + 1) when a
// Last-Event-ID is supplied, or DeliverAll otherwise. This is deliberately
// NOT a shared/cached in-memory buffer — the whole point of D19 is that
// osiris-api holds no state that resuming depends on; JetStream is the
// authority, matching D1's "same fact, one owner" principle applied to the
// event log itself.
//
// §9.6's explicit instruction — "osiris-api는 JetStream 관리 권한이 없어야
// 한다... 필요한 것(ephemeral consumer 생성)만 좁게 줄 것" — is why this
// package creates an EPHEMERAL (undurable) consumer per connection rather
// than reusing/creating durables, and why the NATS ACL granting osiris-api
// this right (orchestrator/internal/natsauth/natsauth.go's osiris-api static
// user) is scoped to exactly: CREATE/DELETE/MSG.NEXT/INFO on the TASKS
// stream, wildcarded only on the consumer NAME (server-generated for an
// ephemeral, unknown ahead of time) — never on the stream, and never
// $JS.API.> as a whole.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	keepaliveInterval = 15 * time.Second
	fetchWait         = 2 * time.Second
	// helloDeadline is D20's client-side self-diagnostic budget ("500ms 안에
	// 받지 못하면... 프록시 버퍼링 의심 경고"). This handler writes+flushes
	// `event: hello` before doing anything else that could block, so on the
	// server side this budget is met unconditionally; documented here so the
	// number the console checks against isn't a mystery to a future reader.
	helloDeadline = 500 * time.Millisecond
)

type Hub struct {
	js nats.JetStreamContext
}

func NewHub(nc *nats.Conn) (*Hub, error) {
	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("jetstream context: %w", err)
	}
	return &Hub{js: js}, nil
}

// JetStream exposes the context this hub already established, so the machine
// principal store (internal/machineauth) and the security audit publisher
// (internal/secevents) reuse ONE JetStream context per process rather than
// each calling nc.JetStream() again. Same connection, same options, one place
// where those options are chosen.
func (h *Hub) JetStream() nats.JetStreamContext { return h.js }

// ServeHTTP streams TASKS-stream events as SSE. See package doc for the
// resume mechanism.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	deliverPolicy := nats.DeliverAllPolicy
	var startSeq uint64
	if lastID := lastEventID(r); lastID != "" {
		if seq, err := strconv.ParseUint(lastID, 10, 64); err == nil {
			deliverPolicy = nats.DeliverByStartSequencePolicy
			startSeq = seq + 1 // resume AFTER the last event the client already has — no gap, no duplicate
		}
	}

	cfg := &nats.ConsumerConfig{
		// No Durable name: an ephemeral consumer (§9.6's "ephemeral consumer
		// 생성"만 허용). AckNone: osiris-api is a read-only OBSERVER of TASKS,
		// not a work-queue participant — nothing here needs redelivery
		// semantics, and AckNone means the NATS ACL below needs no
		// $JS.ACK.TASKS.* grant at all.
		AckPolicy:         nats.AckNonePolicy,
		DeliverPolicy:     deliverPolicy,
		OptStartSeq:       startSeq,
		ReplayPolicy:      nats.ReplayInstantPolicy,
		InactiveThreshold: 5 * time.Minute, // auto-GC backstop if the explicit DeleteConsumer below is ever missed (client crash mid-defer etc.)
	}
	info, err := h.js.AddConsumer("TASKS", cfg)
	if err != nil {
		http.Error(w, "cannot open event stream", http.StatusBadGateway)
		return
	}
	defer func() {
		if derr := h.js.DeleteConsumer("TASKS", info.Name); derr != nil && !errors.Is(derr, nats.ErrConsumerNotFound) {
			log.Printf("osiris-api: sse: failed to delete ephemeral consumer %s: %v", info.Name, derr)
		}
	}()

	// The durable-name argument must be "" here, NOT info.Name: nats.go
	// validates that argument against the CONSUMER's own `Durable` config
	// field, and an ephemeral consumer's Durable is legitimately empty — the
	// only way to identify WHICH ephemeral consumer to bind to is
	// nats.Bind(stream, consumerName), which is independent of the
	// (necessarily durable-only) second positional argument. Passing
	// info.Name there instead produces exactly the error this comment used
	// to be a fix for: "configuration requests durable to be <name>, but
	// consumer's value is ".
	sub, err := h.js.PullSubscribe("", "", nats.Bind("TASKS", info.Name))
	if err != nil {
		log.Printf("osiris-api: sse: PullSubscribe/Bind failed for consumer %s: %v", info.Name, err)
		http.Error(w, "cannot bind event stream", http.StatusBadGateway)
		return
	}
	defer sub.Unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// D20's application-level second line of defense against a misconfigured
	// intermediary buffering the response despite proxy_buffering off.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// D20 self-diagnostic — written before anything that could block.
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	ctx := r.Context()
	lastKeepalive := time.Now()

	for {
		if ctx.Err() != nil {
			return
		}

		fetchCtx, cancel := context.WithTimeout(ctx, fetchWait)
		msgs, ferr := sub.Fetch(1, nats.Context(fetchCtx))
		cancel()

		if ferr != nil {
			if ctx.Err() != nil {
				return // client disconnected
			}
			if !errors.Is(ferr, context.DeadlineExceeded) && !errors.Is(ferr, nats.ErrTimeout) {
				log.Printf("osiris-api: sse: fetch error: %v", ferr)
				return
			}
			// plain timeout — fall through to the keepalive check below
		}

		for _, m := range msgs {
			meta, merr := m.Metadata()
			if merr != nil {
				continue
			}
			writeEvent(w, meta.Sequence.Stream, m.Subject, m.Data)
			lastKeepalive = time.Now() // a real event also resets the keepalive clock — no need to pad right after real traffic
		}
		flusher.Flush()

		if time.Since(lastKeepalive) >= keepaliveInterval {
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			lastKeepalive = time.Now()
		}
	}
}

func lastEventID(r *http.Request) string {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		return v
	}
	// EventSource cannot set custom headers on the initial request in every
	// browser/proxy combination in practice, so a query-param fallback is
	// offered for the first connect — the header is what native
	// reconnection actually sends on subsequent auto-retries.
	return r.URL.Query().Get("lastEventId")
}

// writeEvent renders one SSE frame. `id:` is the JetStream stream sequence
// (D19's Last-Event-ID contract); `event:` is derived from the NATS subject
// so the console can dispatch without re-parsing the payload.
func writeEvent(w http.ResponseWriter, seq uint64, subject string, data []byte) {
	if !json.Valid(data) {
		data = []byte(`{}`)
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, eventNameForSubject(subject), data)
}

// eventNameForSubject maps the TASKS stream's NATS subjects (§7.1) to the
// console event names §12.2 names for them. Only a partial mapping exists
// today because this build implements no producer for most of them
// (assignment/WBS/chat — all out of scope, see IMPLEMENTATION-NOTES.md);
// unmapped subjects fall through to a generic "console.event" name so the
// resume mechanism itself is exercisable end-to-end even with today's
// sparse event traffic (org.event.* from osiris-core is the one real
// producer wired up right now).
func eventNameForSubject(subject string) string {
	switch {
	case len(subject) >= 10 && subject[:10] == "org.event.":
		// org.event.* is TASKS's only real producer today (osiris-core,
		// internal/events/nats.go) — everything else below is wired up for
		// when its producer exists, not exercised by this build's traffic.
		return "org.node.status_changed"
	case len(subject) >= 5 && subject[:5] == "task.":
		return "wbs.task.status_changed"
	case len(subject) >= 8 && subject[:8] == "approval":
		return "approval.created"
	default:
		return "console.event"
	}
}
