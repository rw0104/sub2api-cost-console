package gateway

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
)

type PoolRow struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Protocol string `json:"protocol"`
	routepool.Entry
}

func (e *Engine) PoolStatus() []PoolRow {
	rows := make([]PoolRow, 0, len(e.routes))
	for _, r := range e.routes {
		rows = append(rows, PoolRow{r.ID, r.DisplayName, r.Protocol, e.pool.Get(r.ID)})
	}
	return rows
}
func (e *Engine) selectEgress(harvest int, hasState bool) (int, error) {
	if e.config.EgressMode == "fixed" {
		for i, r := range e.routes {
			if r.ID == e.config.EgressRoute {
				state := e.pool.Get(r.ID).State
				if state == "disabled" || (e.config.PoolEnabled && state == "failed") {
					return 0, errors.New("固定出口已失败/停用，请在代理池手动回收或选择另一个出口")
				}
				return i, nil
			}
		}
		return 0, errors.New("固定出口不存在，请重新选择")
	}
	if e.config.EgressMode != "random" {
		if hasState {
			return harvest, nil
		}
		return 0, nil
	}
	candidates := []int{}
	for i, r := range e.routes {
		st := e.pool.Get(r.ID).State
		if hasState && i == harvest {
			continue
		}
		if st == "disabled" || (e.config.PoolEnabled && (st == "used" || st == "failed")) {
			continue
		}
		candidates = append(candidates, i)
	}
	if len(candidates) == 0 {
		return 0, errors.New("没有可用的独立出口；请补充节点或手动回收。也可明确选择固定出口/采集同出口模式")
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, errors.New("无法随机选择出口")
	}
	return candidates[binary.LittleEndian.Uint64(b[:])%uint64(len(candidates))], nil
}
