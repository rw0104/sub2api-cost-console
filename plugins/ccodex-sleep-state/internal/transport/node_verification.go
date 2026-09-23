package transport

import (
	"context"
	"encoding/json"
	"errors"
)

// VerifyNodes schedules a bounded background job and immediately returns. The
// config bridge's deadline therefore never bounds a whole subscription scan.
func (c *CoreRuntime) VerifyNodes(ctx context.Context, sessionID string, routeIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	item := c.verificationEngine(sessionID)
	if item == nil {
		return errors.New("找不到运行中的会话；先通过插件发送普通 Codex 请求，再刷新核心状态")
	}
	defer c.release(item)
	return item.engine.StartNodeVerification(sessionID, routeIDs)
}

func (c *CoreRuntime) CancelVerification(sessionID string) error {
	item := c.verificationEngine(sessionID)
	if item == nil {
		return errors.New("会话或运行实例已停止，验证任务也已取消")
	}
	defer c.release(item)
	return item.engine.CancelNodeVerification(sessionID)
}

func (c *CoreRuntime) verificationEngine(sessionID string) *coreEngine {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || sessionID == "" {
		return nil
	}
	for _, item := range c.engines {
		if item.retired {
			continue
		}
		data, _ := json.Marshal(item.engine.Status()["sessions"])
		var sessions []struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &sessions)
		for _, s := range sessions {
			if s.ID == sessionID {
				item.refs++
				return item
			}
		}
	}
	return nil
}

// Caller holds c.mu. Reports contain metadata only; no auth/header payloads.
func (c *CoreRuntime) nodeVerificationReportsLocked() []map[string]any {
	result := []map[string]any{}
	for _, item := range c.engines {
		for _, report := range item.engine.NodeVerifications() {
			encoded, _ := json.Marshal(report)
			row := map[string]any{}
			_ = json.Unmarshal(encoded, &row)
			row["account_id"] = item.account
			result = append(result, row)
		}
	}
	return result
}
