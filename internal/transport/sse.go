package transport

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// SSEEvent is a single server-sent event.
type SSEEvent struct {
	// Event is the event name ("message" if unset by the server).
	Event string
	// Data is the concatenated data payload (data lines joined by "\n").
	Data []byte
	// ID is the last event id, if provided.
	ID string
}

// SSEReader parses a Server-Sent Events stream. It tolerates arbitrary chunk
// boundaries (it reads by line) and ignores comment lines and unknown fields.
// It is not safe for concurrent use.
type SSEReader struct {
	r      *bufio.Reader
	closer io.Closer
	err    error
}

// NewSSEReader wraps rc as an SSE stream. Close closes the underlying reader.
func NewSSEReader(rc io.ReadCloser) *SSEReader {
	return &SSEReader{
		// A large buffer avoids "token too long" on big data frames while still
		// reading incrementally.
		r:      bufio.NewReaderSize(rc, 64*1024),
		closer: rc,
	}
}

// Next returns the next event. ok is false at end of stream or on error (check
// Err). A returned event always has a non-empty Data unless the server sent an
// event with empty data.
func (s *SSEReader) Next() (SSEEvent, bool) {
	var (
		ev       SSEEvent
		dataBuf  bytes.Buffer
		haveData bool
		haveAny  bool
	)
	for {
		line, err := s.r.ReadString('\n')
		if len(line) > 0 {
			// Strip trailing \n and optional \r.
			line = strings.TrimRight(line, "\n")
			line = strings.TrimRight(line, "\r")

			if line == "" {
				// Blank line: dispatch the event if we accumulated anything.
				if haveAny {
					if haveData {
						ev.Data = bytes.TrimSuffix(dataBuf.Bytes(), []byte("\n"))
					}
					return ev, true
				}
				// Blank line with nothing accumulated: skip.
				continue
			}
			if strings.HasPrefix(line, ":") {
				// Comment line; ignore.
				continue
			}
			field, value, found := strings.Cut(line, ":")
			if found {
				value = strings.TrimPrefix(value, " ")
			}
			haveAny = true
			switch field {
			case "event":
				ev.Event = value
			case "data":
				dataBuf.WriteString(value)
				dataBuf.WriteByte('\n')
				haveData = true
			case "id":
				ev.ID = value
			default:
				// Unknown field (e.g. "retry"); ignore but keep the event alive.
			}
		}
		if err != nil {
			if err != io.EOF {
				s.err = err
			}
			// Flush a final event that had no trailing blank line.
			if haveAny {
				if haveData {
					ev.Data = bytes.TrimSuffix(dataBuf.Bytes(), []byte("\n"))
				}
				return ev, true
			}
			return SSEEvent{}, false
		}
	}
}

// Err returns a terminal read error, if any (io.EOF is not reported).
func (s *SSEReader) Err() error { return s.err }

// Close closes the underlying stream.
func (s *SSEReader) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}
