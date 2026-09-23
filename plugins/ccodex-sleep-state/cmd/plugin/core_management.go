package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/transport"
)

func (p *plugin) ensureCore() (*transport.CoreRuntime, error) {
	p.coreMu.Lock()
	defer p.coreMu.Unlock()
	if p.core != nil {
		return p.core, nil
	}
	if p.pool == nil {
		pool, err := p.openPool()
		if err != nil {
			return nil, err
		}
		p.pool = pool
	}
	p.core = transport.NewCoreRuntime(p.registryFor, p.invalidateRegistry, p.pool)
	p.core.UpdatePolicies(p.config.Load())
	return p.core, nil
}

func (p *plugin) inspectCore(ctx context.Context, next *config.Config, request config.CoreRequest) *config.CoreReport {
	report := &config.CoreReport{Schema: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Sessions: []config.CoreSession{}, Pool: []config.CorePoolNode{}}
	core, err := p.ensureCore()
	if err != nil {
		report.ErrorCode, report.Message = "CORE_STORAGE_UNAVAILABLE", "无法读取节点池记录。请检查插件安装目录权限，保留原记录后修复。"
		return report
	}
	if request.Operation == "retry" {
		err = core.Retry(ctx, request.SessionID, request.RouteID)
		if err != nil {
			report.ErrorCode, report.Message = "CORE_RETRY_REJECTED", "本次采集未完成。请查看会话的采集冷却、上游暂停和错误原因；需要先有实际请求提供的运行会话。"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				report.ErrorCode, report.Message = "CORE_TIMEOUT", "本轮采集达到时间上限，请查看已完成的状态；正式请求没有被重放。"
			}
		}
	}
	if request.Operation == "verify_nodes" {
		if err := core.VerifyNodes(ctx, request.SessionID, request.RouteIDs); err != nil {
			report.ErrorCode, report.Message = "NODE_VERIFICATION_REJECTED", "无法启动节点请求头验证。请先让一次 OAuth 请求经过插件，并检查当前会话是否正忙或受上游暂停限制。"
		} else {
			report.Message = "已开始逐节点请求头验证，将按每轮上限和冷却分批执行。点击刷新查看进度；可随时取消。"
		}
	}
	if request.Operation == "cancel_verification" {
		if err := core.CancelVerification(request.SessionID); err != nil {
			report.ErrorCode, report.Message = "NODE_VERIFICATION_NOT_FOUND", "没有找到运行中的节点验证，请刷新状态。"
		} else {
			report.Message = "已取消该会话的节点验证，已完成结果保留。"
		}
	}
	if request.Operation == "pool" {
		registry, sourceErr := transport.NewRegistryFromConfig(ctx, next, "")
		if sourceErr != nil {
			report.ErrorCode, report.Message = "CORE_SOURCE_UNAVAILABLE", "无法确认当前节点来源，请先获取节点后重试。"
		} else {
			defer registry.Close()
			valid := true
			for _, id := range request.RouteIDs {
				if _, _, exists := registry.ByID(id); !exists {
					valid = false
				}
			}
			if !valid {
				report.ErrorCode, report.Message = "CORE_ROUTE_MISSING", "部分节点已不在当前来源中，请重新获取节点。"
			} else if err := p.pool.Change(request.RouteIDs, request.Action, "manual_"+request.Action, false); err != nil {
				report.ErrorCode, report.Message = "CORE_POOL_WRITE_FAILED", "节点池变更未保存，请检查安装目录权限或节点池记录。"
			}
		}
	}
	// Decode into an explicit display schema: upstream state/credentials and
	// arbitrary request headers can never be persisted through this report.
	raw, _ := json.Marshal(core.Status())
	var current config.CoreReport
	_ = json.Unmarshal(raw, &current)
	var labels struct {
		Pool []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"pool"`
	}
	_ = json.Unmarshal(raw, &labels)
	labelByID := map[string]string{}
	for _, row := range labels.Pool {
		labelByID[row.ID] = row.Label
	}
	report.Engines, report.RequestsTotal = current.Engines, current.RequestsTotal
	report.NodeVerifications = current.NodeVerifications
	if current.Sessions != nil {
		report.Sessions = current.Sessions
	}
	byID := map[string]config.CorePoolNode{}
	for _, node := range current.Pool {
		if node.Name == "" {
			node.Name = labelByID[node.ID]
		}
		byID[node.ID] = node
	}
	if next.RouteReport != nil {
		for _, node := range next.RouteReport.Nodes {
			byID[node.ID] = config.CorePoolNode{ID: node.ID, Name: node.Name, Protocol: node.Protocol}
		}
	}
	for _, node := range byID {
		entry := p.pool.Get(node.ID)
		node.State, node.Attempts, node.Reason = entry.State, entry.Attempts, entry.Reason
		report.Pool = append(report.Pool, node)
	}
	sort.Slice(report.Pool, func(i, j int) bool { return report.Pool[i].ID < report.Pool[j].ID })
	if p.pool.Err() != nil {
		report.ErrorCode, report.Message = "CORE_STORAGE_UNAVAILABLE", "节点池记录不可用，已停止自动使用。请保留原记录并检查文件内容与权限后重试。"
	}
	if report.Message == "" && len(report.Sessions) == 0 {
		report.Message = "当前进程尚无运行会话。启用插件并让 OAuth 请求命中保护传输后，才能显示采集与注入状态。"
	}
	return report
}
