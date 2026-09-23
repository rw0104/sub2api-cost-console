package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/transport"
)

func (p *plugin) inspectRoutes(ctx context.Context, next *config.Config, request config.RouteRequest) *config.RouteReport {
	report := &config.RouteReport{Schema: 1, SourceSignature: next.SourceSignature(), GeneratedAt: time.Now().UTC().Format(time.RFC3339), Nodes: []config.RouteNode{}}
	registry, err := transport.NewRegistryFromConfig(ctx, next, "")
	if err != nil {
		report.ErrorCode = routeSourceErrorCode(ctx, err)
		return report
	}
	defer registry.Close()
	return p.inspectRegistry(ctx, next, request, registry, report)
}

func (p *plugin) inspectRegistry(ctx context.Context, next *config.Config, request config.RouteRequest, registry *transport.Registry, report *config.RouteReport) *config.RouteReport {
	previous := make(map[string]config.RouteNode)
	if cached := config.SanitizeRouteReport(next.RouteReport, report.SourceSignature); cached != nil {
		for _, node := range cached.Nodes {
			previous[node.ID] = node
		}
	}
	for _, node := range registry.Nodes() {
		row := config.RouteNode{ID: node.ID, Name: node.Name, Protocol: node.Protocol, Status: "untested"}
		if old, ok := previous[node.ID]; ok {
			row.Status, row.LatencyMS, row.HTTPStatus, row.ErrorCode = old.Status, old.LatencyMS, old.HTTPStatus, old.ErrorCode
		}
		report.Nodes = append(report.Nodes, row)
	}
	if request.Operation == "test" {
		checks, err := transport.CheckRoutes(ctx, registry, request.RouteID)
		byID := make(map[string]int, len(report.Nodes))
		for index, node := range report.Nodes {
			byID[node.ID] = index
		}
		for _, result := range checks.Rows {
			index, ok := byID[result.ID]
			if !ok {
				continue
			}
			row := &report.Nodes[index]
			row.Status, row.LatencyMS, row.HTTPStatus, row.ErrorCode = result.Status, result.LatencyMS, result.HTTPStatus, result.ErrorCode
		}
		p.lastTest.Store(&routeTestStatus{total: checks.Total, available: checks.Available, at: time.Now()})
		if err != nil || ctx.Err() != nil {
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				report.ErrorCode = "TEST_TIMEOUT"
			case errors.Is(ctx.Err(), context.Canceled):
				report.ErrorCode = "TEST_CANCELLED"
			case request.RouteID != "":
				if _, _, exists := registry.ByID(request.RouteID); !exists {
					report.ErrorCode = "ROUTE_UNAVAILABLE"
				} else {
					report.ErrorCode = "NO_AVAILABLE_ROUTES"
				}
			default:
				report.ErrorCode = "NO_AVAILABLE_ROUTES"
			}
		}
	}
	if next.RouteMode == "fixed" {
		if _, _, exists := registry.ByID(next.FixedRouteID); !exists {
			report.ErrorCode = "ROUTE_UNAVAILABLE"
		}
	}
	return config.SanitizeRouteReport(report, report.SourceSignature)
}

// Source loaders intentionally hide credentials. The UI additionally receives
// only these stable categories, regardless of future dependency error changes.
func routeSourceErrorCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "SOURCE_TIMEOUT"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "SOURCE_CANCELLED"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "exceed 256"):
		return "ROUTE_LIMIT_EXCEEDED"
	case strings.Contains(message, "proxy environment source is empty"):
		return "PROXY_ENV_EMPTY"
	case strings.Contains(message, "subscription"):
		switch {
		case strings.Contains(message, "environment variable is empty"):
			return "SUBSCRIPTION_ENV_EMPTY"
		case strings.Contains(message, "download failed"):
			return "SUBSCRIPTION_DOWNLOAD_FAILED"
		case strings.Contains(message, "returned http"):
			return "SUBSCRIPTION_HTTP_ERROR"
		case strings.Contains(message, "file cannot"):
			return "SUBSCRIPTION_FILE_ERROR"
		case strings.Contains(message, "exceeds 2 mib"):
			return "SUBSCRIPTION_TOO_LARGE"
		case strings.Contains(message, "no nodes"):
			return "SUBSCRIPTION_EMPTY"
		default:
			return "SUBSCRIPTION_INVALID"
		}
	default:
		return "ROUTE_SOURCE_INVALID"
	}
}
