package gateway

import (
	"bytes"
	"strings"
)

// walkSSE handles CRLF and multi-line data without retaining anything beyond
// the caller's bounded byte buffer. Callback slices are ephemeral.
func walkSSE(data []byte, visit func(string, []byte)) {
	var name string
	var payload []byte
	flush := func() {
		if len(payload) > 0 {
			visit(name, payload)
		}
		name = ""
		payload = nil
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			flush()
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			name = strings.TrimSpace(string(line[6:]))
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			if len(payload) > 0 {
				payload = append(payload, '\n')
			}
			payload = append(payload, bytes.TrimPrefix(line[5:], []byte(" "))...)
		}
	}
	flush()
}
