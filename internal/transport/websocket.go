package transport

import "errors"

// ErrWebSocketUnsupported is returned by realtime/live transport entry points.
// Bidirectional WebSocket transport (for provider live APIs) is planned but not
// implemented in this release (GO_AGENT_PLAN.md non-goals; §7.3).
var ErrWebSocketUnsupported = errors.New("transport: websocket/live transport not implemented in this build")
