// Package requestdiag keeps numeric HTTP diagnostics without retaining request
// bodies, credentials, or JSON values in a request context.
package requestdiag

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
)

type contextKey struct{}

// Snapshot distinguishes measured zeroes from fields this path did not observe.
// Request bytes refer to the normalized UTF-8 body returned by the body reader,
// not compressed wire bytes. Field sizes include JSON quotes/escapes and sum
// repeated top-level keys; surrounding whitespace and field names are excluded.
// UpstreamBodyBytes is the most recent native HTTP dispatch's ContentLength.
type Snapshot struct {
	RequestObserved    bool
	JSONFieldsObserved bool
	RequestBodyBytes   int64
	InputBytes         int64
	ToolsBytes         int64
	InstructionsBytes  int64
	UpstreamObserved   bool
	UpstreamIncomplete bool
	UpstreamBodyKnown  bool
	UpstreamBodyBytes  int64
	UpstreamAttempts   int64
}

type state struct {
	mu sync.Mutex
	Snapshot
}

// WithContext starts one accumulator shared by context-derived retry requests.
func WithContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, &state{})
}

func fromContext(ctx context.Context) *state {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(contextKey{}).(*state)
	return s
}

// RecordRequest observes the first successfully read body. It never reads an
// http.Request.Body or stores a reference to body, including when JSON is invalid.
func RecordRequest(ctx context.Context, body []byte) {
	s := fromContext(ctx)
	if s == nil {
		return
	}
	s.mu.Lock()
	alreadyObserved := s.RequestObserved
	s.mu.Unlock()
	if alreadyObserved {
		return
	}
	input, tools, instructions, valid := measureFields(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.RequestObserved {
		s.RequestObserved = true
		s.JSONFieldsObserved = valid
		s.RequestBodyBytes = int64(len(body))
		s.InputBytes, s.ToolsBytes, s.InstructionsBytes = input, tools, instructions
	}
}

// BeginUpstream marks a path with actual dispatch instrumentation, so a local
// preprocessing rejection can report zero attempts instead of missing data.
func BeginUpstream(ctx context.Context) {
	if s := fromContext(ctx); s != nil {
		s.mu.Lock()
		s.UpstreamObserved = true
		s.mu.Unlock()
	}
}

// RecordDispatch counts handoffs to the native HTTP transport, including failed
// connections. It does not claim delivery or count the transport's own retries.
func RecordDispatch(ctx context.Context, contentLength int64) {
	if s := fromContext(ctx); s != nil {
		s.mu.Lock()
		s.UpstreamObserved = true
		s.UpstreamAttempts++
		s.UpstreamBodyKnown = contentLength >= 0
		if s.UpstreamBodyKnown {
			s.UpstreamBodyBytes = contentLength
		}
		s.mu.Unlock()
	}
}

// OmitUpstream invalidates the entire request's dispatch summary when a plugin
// owns transport. Later native attempts must not turn partial counts into totals.
func OmitUpstream(ctx context.Context) {
	if s := fromContext(ctx); s != nil {
		s.mu.Lock()
		s.UpstreamIncomplete = true
		s.mu.Unlock()
	}
}

func Load(ctx context.Context) Snapshot {
	if s := fromContext(ctx); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.Snapshot
	}
	return Snapshot{}
}

// Validation and scanning work on the existing byte slice. Only escaped field
// names up to 74 bytes can be decoded; a large input/tools value is never copied.
func measureFields(body []byte) (input, tools, instructions int64, valid bool) {
	if !json.Valid(body) {
		return 0, 0, 0, false
	}
	pos := skipSpace(body, 0)
	if body[pos] != '{' {
		return 0, 0, 0, false
	}
	pos++
	for {
		pos = skipSpace(body, pos)
		if body[pos] == '}' {
			return input, tools, instructions, true
		}
		keyStart := pos
		pos = stringEnd(body, pos)
		key := fieldKey(body[keyStart:pos])
		pos = skipSpace(body, pos) + 1 // validated colon
		pos = skipSpace(body, pos)
		end := valueEnd(body, pos)
		size := int64(end - pos)
		switch key {
		case "input":
			input += size
		case "tools":
			tools += size
		case "instructions":
			instructions += size
		}
		pos = skipSpace(body, end)
		if body[pos] == ',' {
			pos++
		}
	}
}

func fieldKey(raw []byte) string {
	if len(raw) > 74 {
		return ""
	}
	switch string(raw) {
	case `"input"`:
		return "input"
	case `"tools"`:
		return "tools"
	case `"instructions"`:
		return "instructions"
	}
	// Even a fully unicode-escaped "instructions" key is only 74 bytes.
	if !bytes.ContainsRune(raw, '\\') {
		return ""
	}
	var key string
	if json.Unmarshal(raw, &key) != nil {
		return ""
	}
	return key
}

func skipSpace(body []byte, pos int) int {
	for pos < len(body) {
		switch body[pos] {
		case ' ', '\t', '\r', '\n':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func stringEnd(body []byte, pos int) int {
	for pos++; pos < len(body); pos++ {
		switch body[pos] {
		case '\\':
			pos++
		case '"':
			return pos + 1
		}
	}
	return pos
}

func valueEnd(body []byte, pos int) int {
	if body[pos] == '"' {
		return stringEnd(body, pos)
	}
	if body[pos] == '{' || body[pos] == '[' {
		depth := 1
		for pos++; pos < len(body); pos++ {
			switch body[pos] {
			case '"':
				pos = stringEnd(body, pos) - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return pos + 1
				}
			}
		}
		return pos
	}
	for pos < len(body) {
		switch body[pos] {
		case ',', '}', ' ', '\t', '\r', '\n':
			return pos
		}
		pos++
	}
	return pos
}
