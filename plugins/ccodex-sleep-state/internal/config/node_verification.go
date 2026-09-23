package config

type NodeVerification struct {
	SessionID string            `json:"session_id"`
	AccountID int64             `json:"account_id"`
	Model     string            `json:"model"`
	State     string            `json:"state"`
	Total     int               `json:"total"`
	Completed int               `json:"completed"`
	NextRunAt string            `json:"next_run_at,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Rows      []NodeStateResult `json:"rows"`
}

// A qualification result never contains an authentication credential, token,
// arbitrary upstream body, or proxy URL. It cannot authorize routing itself.
type NodeStateResult struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Status         string `json:"status"`
	HeaderPresent  bool   `json:"header_present"`
	Parsed         bool   `json:"parsed"`
	Qualified      bool   `json:"qualified"`
	ExpectedLength int    `json:"expected_length"`
	ObservedLength int    `json:"observed_length"`
	ExpectedBlocks int    `json:"expected_blocks"`
	ObservedBlocks int    `json:"observed_blocks"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	LatencyMS      int64  `json:"latency_ms,omitempty"`
	CheckedAt      string `json:"checked_at,omitempty"`
	Reason         string `json:"reason,omitempty"`
}
