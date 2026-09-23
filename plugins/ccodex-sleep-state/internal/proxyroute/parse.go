// Package proxyroute parses the safe subset of proxy sources accepted by the
// plugin. It never starts a listener, TUN device, or system network controller.
package proxyroute

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxSubscriptionBytes = 2 << 20
	MaxNodes             = 256
)

type Node struct {
	Name     string
	Scheme   string
	Host     string
	Port     int
	Username string
	Password string
}

func (n Node) URL() (string, error) {
	if n.Host == "" || n.Port < 1 || n.Port > 65535 {
		return "", errors.New("proxy node requires a host and explicit port")
	}
	u := url.URL{Scheme: strings.ToLower(n.Scheme), Host: net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}
	if n.Username != "" {
		if n.Password != "" {
			u.User = url.UserPassword(n.Username, n.Password)
		} else {
			u.User = url.User(n.Username)
		}
	}
	return u.String(), nil
}

func Parse(data []byte) ([]Node, error) {
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
		nodes := make([]Node, 0, len(*document.Proxies))
		for index, raw := range *document.Proxies {
			node, err := parseMapNode(raw)
			if err != nil {
				return nil, fmt.Errorf("subscription node %d: %w", index+1, err)
			}
			nodes = append(nodes, node)
		}
		return deduplicate(nodes)
	}

	if !bytes.Contains(data, []byte("://")) {
		decoded, err := decodeBase64(data)
		if err != nil {
			return nil, errors.New("expected proxy YAML or a URI/Base64 subscription")
		}
		data = decoded
	}
	var nodes []Node
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
	return deduplicate(nodes)
}

func ParseURI(raw string) (Node, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" || u.User != nil && u.User.Username() == "" {
		return Node{}, errors.New("invalid proxy URI")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h" {
		return Node{}, errors.New("unsupported proxy URI")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return Node{}, errors.New("proxy URI requires a host and explicit port")
	}
	if (scheme == "http" || scheme == "https") && u.User != nil {
		return Node{}, errors.New("HTTP proxy credentials are not accepted")
	}
	node := Node{Name: "imported", Scheme: scheme, Host: u.Hostname(), Port: port}
	if u.User != nil {
		node.Username = u.User.Username()
		node.Password, _ = u.User.Password()
	}
	return node, nil
}

func parseMapNode(value map[string]any) (Node, error) {
	if err := validateFields(value); err != nil {
		return Node{}, err
	}
	typ, _ := value["type"].(string)
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "https" {
		typ = "http"
		value["tls"] = true
	}
	if typ != "http" && typ != "socks5" {
		return Node{}, errors.New("unsupported protocol in subscription")
	}
	server, _ := value["server"].(string)
	server = strings.TrimSpace(server)
	port, err := number(value["port"])
	if err != nil || server == "" || port < 1 || port > 65535 {
		return Node{}, errors.New("proxy node requires a host and explicit port")
	}
	name, _ := value["name"].(string)
	username, _ := value["username"].(string)
	password, _ := value["password"].(string)
	if typ == "http" {
		if tlsValue, ok := value["tls"]; ok && isTruthy(tlsValue) {
			typ = "https"
		}
	} else if tlsValue, ok := value["tls"]; ok && isTruthy(tlsValue) {
		return Node{}, errors.New("TLS SOCKS5 subscription nodes are not supported")
	}
	return Node{Name: name, Scheme: typ, Host: server, Port: port, Username: username, Password: password}, nil
}

func number(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case uint64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, errors.New("port must be an integer")
		}
		return int(v), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	default:
		return 0, errors.New("port must be an integer")
	}
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

func deduplicate(nodes []Node) ([]Node, error) {
	seen := make(map[string]struct{}, len(nodes))
	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		key, err := node.URL()
		if err != nil {
			return nil, err
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, node)
	}
	if len(out) == 0 {
		return nil, errors.New("subscription contains no supported nodes")
	}
	return out, nil
}

func validateFields(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch strings.ToLower(key) {
			case "dialer-proxy", "interface-name", "routing-mark", "certificate", "private-key", "private-key-passphrase", "ca", "ca-str":
				return errors.New("subscription contains a forbidden local-file or routing override")
			case "skip-cert-verify":
				if isTruthy(child) {
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

func isTruthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}
