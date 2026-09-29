package supervisor

import (
	"bufio"
	"io"
)

// logChanCapacity is the buffered-channel depth from §5.4:
//
//	"헬스서버는 자식 stdout 파이프에 절대 블록되지 않는다: 별도 goroutine +
//	 버퍼드 채널, 채널 만재 시 로그 라인을 버린다 (로그 유실 < 재시작)."
//
// 4096 lines is enough to absorb a normal burst of stream-json (tool results
// can be large but not sustained at this rate); anything beyond that is a
// runaway child, and the design has already decided what to do about a
// runaway child: drop its logs, keep the process alive.
const logChanCapacity = 4096

// maxLogLineBytes caps a single line. stream-json tool_result payloads can be
// megabytes; a 1 MiB cap keeps one pathological line from becoming the whole
// channel's memory. Over-long lines are truncated with a marker, never
// dropped silently, because the transcript is an audit artifact.
const maxLogLineBytes = 1 << 20

// LogLine is one line of child stdout/stderr on its way to NATS and the
// transcript file.
type LogLine struct {
	Stream string // "stdout" | "stderr"
	Text   string
}

// LogPipe is a NON-BLOCKING fan-in. The only send operation is a select with
// a default branch, so no producer can ever block on it — which is what makes
// the §5.4 invariant hold. Note what is NOT here: no mutex (a mutex on the
// send path could be held by a slow consumer, reintroducing exactly the
// coupling the design forbids) and no unbounded queue (which would trade a
// restart for an OOM kill — strictly worse, since an OOM kill loses the
// checkpoint write too).
type LogPipe struct {
	ch chan LogLine
	// onDrop is called once per dropped line. It takes a callback rather than a
	// *atomic.Int64 because of a bug this cost real debugging time to find: the
	// first version incremented a counter that lived on the Supervisor, while
	// /livez reported a DIFFERENT counter on health.State that nothing ever
	// wrote. A flood test then "passed" with droppedLogLines:0 while 7.9 million
	// of 8 million lines had in fact been discarded — the invariant was working
	// and its only evidence was silently broken. Routing the increment through
	// the health state's own atomic makes the reported number the same number,
	// by construction.
	onDrop func()
}

func NewLogPipe(onDrop func()) *LogPipe {
	if onDrop == nil {
		onDrop = func() {}
	}
	return &LogPipe{ch: make(chan LogLine, logChanCapacity), onDrop: onDrop}
}

func (p *LogPipe) C() <-chan LogLine { return p.ch }

// TrySend never blocks. On a full channel the line is DROPPED and counted.
// The counter is surfaced in /livez, /statusz and the heartbeat so that
// "logs were lost" is an observable fact rather than a silent one.
func (p *LogPipe) TrySend(l LogLine) {
	select {
	case p.ch <- l:
	default:
		p.onDrop()
	}
}

// Pump reads r line by line and TrySends each line. It runs in its own
// goroutine per stream and its ONLY job is to keep the child's pipe drained:
// if this goroutine ever blocked, the child would block on write(2) once the
// 64 KiB pipe buffer filled, and a blocked child that the supervisor is
// waiting on is a wedged turn. So the reader must be strictly faster than the
// writer, which it is, because its downstream send cannot block.
//
// Returns when r hits EOF (child closed the pipe / exited).
func (p *LogPipe) Pump(stream string, r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := readLine(br)
		if line != "" {
			p.TrySend(LogLine{Stream: stream, Text: line})
		}
		if err != nil {
			return
		}
	}
}

// readLine reads one \n-terminated line of arbitrary length, truncating at
// maxLogLineBytes. bufio.Scanner is avoided on purpose: its default 64 KiB
// token limit turns an over-long stream-json line into a hard error that ends
// the scan, which would silently truncate the REST of the transcript rather
// than just that one line.
func readLine(br *bufio.Reader) (string, error) {
	var buf []byte
	truncated := false
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			if len(buf) < maxLogLineBytes {
				room := maxLogLineBytes - len(buf)
				if len(chunk) > room {
					buf = append(buf, chunk[:room]...)
					truncated = true
				} else {
					buf = append(buf, chunk...)
				}
			} else {
				truncated = true
			}
		}
		if err == bufio.ErrBufferFull {
			continue // long line — keep reading
		}
		out := string(trimNewline(buf))
		if truncated {
			out += "...[truncated by supervisor: line exceeded 1MiB]"
		}
		return out, err
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
