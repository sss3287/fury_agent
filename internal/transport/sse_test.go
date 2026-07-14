package transport

import (
	"io"
	"strings"
	"testing"
)

// chunkedReader delivers the payload in fixed-size pieces to simulate arbitrary
// network chunk boundaries.
type chunkedReader struct {
	data []byte
	size int
	pos  int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := r.pos + r.size
	if end > len(r.data) {
		end = len(r.data)
	}
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

func (r *chunkedReader) Close() error { return nil }

func collect(t *testing.T, raw string, chunk int) []SSEEvent {
	t.Helper()
	r := NewSSEReader(&chunkedReader{data: []byte(raw), size: chunk})
	var out []SSEEvent
	for {
		ev, ok := r.Next()
		if !ok {
			break
		}
		out = append(out, ev)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("reader err: %v", err)
	}
	return out
}

func TestSSEBasic(t *testing.T) {
	raw := "event: message\ndata: hello\n\nevent: done\ndata: {\"x\":1}\n\n"
	for _, chunk := range []int{1, 3, 7, 1024} {
		evs := collect(t, raw, chunk)
		if len(evs) != 2 {
			t.Fatalf("chunk %d: got %d events, want 2", chunk, len(evs))
		}
		if evs[0].Event != "message" || string(evs[0].Data) != "hello" {
			t.Fatalf("chunk %d: bad event0: %+v", chunk, evs[0])
		}
		if evs[1].Event != "done" || string(evs[1].Data) != `{"x":1}` {
			t.Fatalf("chunk %d: bad event1: %+v", chunk, evs[1])
		}
	}
}

func TestSSEMultilineDataAndComments(t *testing.T) {
	raw := ": this is a comment\ndata: line1\ndata: line2\n\n"
	evs := collect(t, raw, 2)
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if string(evs[0].Data) != "line1\nline2" {
		t.Fatalf("bad multiline data: %q", string(evs[0].Data))
	}
}

func TestSSENoTrailingBlankLine(t *testing.T) {
	// A final event without a trailing blank line must still be delivered.
	raw := "data: last\n"
	evs := collect(t, raw, 5)
	if len(evs) != 1 || string(evs[0].Data) != "last" {
		t.Fatalf("bad final event: %+v", evs)
	}
}

func TestSSEDataOSSEStylePrefix(t *testing.T) {
	// OpenAI-style terminal sentinel is passed through as data; callers detect it.
	raw := "data: [DONE]\n\n"
	evs := collect(t, raw, 4)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), "[DONE]") {
		t.Fatalf("bad sentinel: %+v", evs)
	}
}
