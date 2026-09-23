package mihomoroute

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/metacubex/mihomo/common/convert"
	"gopkg.in/yaml.v3"
)

const (
	MaxSubscriptionBytes = 2 << 20
	MaxNodes             = 256
)

func Parse(data []byte) ([]map[string]any, error) {
	if len(data) > MaxSubscriptionBytes {
		return nil, errors.New("subscription exceeds 2 MiB")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("subscription is empty")
	}
	var document struct {
		Proxies *[]map[string]any `yaml:"proxies"`
	}
	if yaml.Unmarshal(data, &document) == nil && document.Proxies != nil {
		if len(*document.Proxies) == 0 {
			return nil, errors.New("subscription has an empty proxies list")
		}
		if len(*document.Proxies) > MaxNodes {
			return nil, errors.New("subscription exceeds 256 nodes")
		}
		return *document.Proxies, nil
	}
	if !bytes.Contains(data, []byte("://")) {
		decoded, err := decodeBase64(data)
		if err != nil {
			return nil, errors.New("expected proxy YAML or a URI/Base64 subscription")
		}
		data = decoded
	}
	var nodes []map[string]any
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		node, err := ParseURI(line)
		if err != nil {
			return nil, fmt.Errorf("subscription line %d: invalid or unsupported proxy URI", lineNumber+1)
		}
		nodes = append(nodes, node)
		if len(nodes) > MaxNodes {
			return nil, errors.New("subscription exceeds 256 nodes")
		}
	}
	if len(nodes) == 0 {
		return nil, errors.New("subscription contains no supported nodes")
	}
	return nodes, nil
}

func ParseURI(raw string) (map[string]any, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return nil, errors.New("invalid proxy URI")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "http" || scheme == "https" || scheme == "socks5" || scheme == "socks5h" {
		portText := u.Port()
		// URL serializers omit default HTTP ports. Restore them before node
		// identity is calculated so saving in the UI cannot change a route.
		if portText == "" && !strings.HasSuffix(u.Host, ":") {
			switch scheme {
			case "http":
				portText = "80"
			case "https":
				portText = "443"
			}
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("proxy URI requires a valid host and port (SOCKS ports must be explicit)")
		}
		kind := scheme
		if scheme == "https" {
			kind = "http"
		}
		if scheme == "socks5h" {
			kind = "socks5"
		}
		node := map[string]any{"name": "imported", "type": kind, "server": u.Hostname(), "port": port}
		if scheme == "https" {
			node["tls"] = true
		}
		if u.User != nil {
			node["username"] = u.User.Username()
			node["password"], _ = u.User.Password()
		}
		return node, nil
	}
	nodes, err := convert.ConvertsV2Ray([]byte(raw))
	if err != nil || len(nodes) != 1 {
		return nil, errors.New("unsupported proxy URI")
	}
	return nodes[0], nil
}

func decodeBase64(data []byte) ([]byte, error) {
	compact := strings.Join(strings.Fields(string(data)), "")
	var last error
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
		last = err
	}
	return nil, last
}

func ValidateNode(node map[string]any) error {
	switch node["type"] {
	case "http", "socks5", "ss", "ssr", "vmess", "vless", "trojan", "hysteria", "hysteria2", "tuic", "anytls":
	default:
		return errors.New("unsupported protocol in subscription")
	}
	return validateFields(node)
}

func validateFields(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch strings.ToLower(key) {
			case "dialer-proxy", "interface-name", "routing-mark", "certificate", "private-key", "private-key-passphrase", "ca", "ca-str":
				return errors.New("subscription contains a forbidden local-file or routing override")
			case "skip-cert-verify":
				if child != false && child != "false" && child != nil {
					return errors.New("insecure TLS is not allowed")
				}
			}
			if err := validateFields(child); err != nil {
				return err
			}
		}
	case map[any]any:
		for key, child := range v {
			if keyText, ok := key.(string); ok {
				if err := validateFields(map[string]any{keyText: child}); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, child := range v {
			if err := validateFields(child); err != nil {
				return err
			}
		}
	}
	return nil
}
