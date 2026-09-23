// Package turnstate treats encrypted state as opaque. Shape is a heuristic,
// never a signature check or a measurement of model quality.
package turnstate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const Header = "X-Codex-Turn-State"

type Policy struct {
	Blocks       int
	TTL, Refresh time.Duration
}

type Token struct {
	Value, Fingerprint string
	Issued             time.Time
	Blocks             int
}

func Parse(value string) (Token, error) {
	var token Token
	value = strings.TrimSpace(value)
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n\t ") {
		return token, errors.New("invalid state encoding")
	}
	core := strings.TrimRight(value, "=")
	if len(value)-len(core) > 2 {
		return token, errors.New("invalid state padding")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(core)
	if err != nil || len(raw) < 73 || raw[0] != 0x80 || (len(raw)-57)%16 != 0 {
		return token, errors.New("unrecognized state envelope")
	}
	issued := binary.BigEndian.Uint64(raw[1:9])
	if issued < 1577836800 || issued >= 4102444800 {
		return token, errors.New("state timestamp out of range")
	}
	sum := sha256.Sum256([]byte(value))
	return Token{Value: value, Fingerprint: hex.EncodeToString(sum[:8]), Issued: time.Unix(int64(issued), 0), Blocks: (len(raw) - 57) / 16}, nil
}

func (p Policy) Accept(token Token, now time.Time) bool {
	return token.Value != "" && token.Blocks == p.Blocks && !token.Issued.After(now.Add(30*time.Second)) && now.Before(token.Issued.Add(p.TTL-30*time.Second))
}

func BlocksForEncodedLength(length int) (int, bool) {
	for blocks := 1; blocks <= 92; blocks++ {
		rawLength := 57 + 16*blocks
		if ((rawLength + 2) / 3 * 4) == length {
			return blocks, true
		}
	}
	return 0, false
}

func Normalize(raw string, targetLength int) string {
	value := strings.TrimSpace(raw)
	if len(value) != targetLength {
		return ""
	}
	blocks, ok := BlocksForEncodedLength(targetLength)
	if !ok {
		return ""
	}
	token, err := Parse(value)
	if err != nil || token.Blocks != blocks {
		return ""
	}
	return token.Value
}

func Digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
