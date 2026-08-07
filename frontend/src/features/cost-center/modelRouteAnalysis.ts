import type { AdminUsageLog } from '@/types'
import type { Channel, ChannelModelPricing } from '@/api/admin/channels'
import { classifyModelProvider } from './modelCostAnalysis'

export interface ModelRouteRow {
  key: string
  requestedModel: string
  upstreamModel: string
  upstreamResponseModel: string
  upstreamModelMismatch: boolean | null
  modelAuditStatus: 'matched' | 'mismatch' | 'unobserved'
  mappingChain: string
  provider: string
  accountId: number | null
  accountName: string
  channelId: number | null
  channelName: string
  groupId: number | null
  groupName: string
  inboundEndpoint: string
  upstreamEndpoint: string
  channelPricing: ChannelModelPricing | null
  requests: number
  inputTokens: number
  cacheReadTokens: number
  outputTokens: number
  standardCost: number
  accountCost: number
  revenue: number
  firstSeen: string
  lastSeen: string
}

function numeric(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function text(value: unknown, fallback: string): string {
  const normalized = String(value ?? '').trim()
  return normalized || fallback
}

function modelPatternMatches(model: string, pattern: string): boolean {
  const escaped = pattern.trim().replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*')
  return escaped !== '' && new RegExp(`^${escaped}$`, 'i').test(model)
}

function findChannelPricing(channel: Channel | undefined, model: string): ChannelModelPricing | null {
  if (!channel) return null
  return channel.model_pricing.find((pricing) => pricing.models.some((pattern) => modelPatternMatches(model, pattern))) ?? null
}

export function buildModelRouteRows(logs: AdminUsageLog[], channels: Channel[]): ModelRouteRow[] {
  const channelNames = new Map(channels.map((channel) => [channel.id, channel.name]))
  const channelById = new Map(channels.map((channel) => [channel.id, channel]))
  const rows = new Map<string, ModelRouteRow>()

  for (const log of logs) {
    const requestedModel = text(log.model, '未知请求模型')
    const upstreamModel = text(log.upstream_model, requestedModel)
    const upstreamResponseModel = text(log.upstream_response_model, '')
    const upstreamModelMismatch = log.upstream_model_mismatch == null ? null : Boolean(log.upstream_model_mismatch)
    const modelAuditStatus = upstreamModelMismatch == null
      ? 'unobserved'
      : upstreamModelMismatch ? 'mismatch' : 'matched'
    const accountId = log.account_id == null ? null : Number(log.account_id)
    const channelId = log.channel_id == null ? null : Number(log.channel_id)
    const inboundEndpoint = text(log.inbound_endpoint, '未记录')
    const upstreamEndpoint = text(log.upstream_endpoint, '未记录')
    const key = [requestedModel, upstreamModel, upstreamResponseModel, modelAuditStatus, accountId ?? 'deleted', channelId ?? 'direct', inboundEndpoint, upstreamEndpoint].join('|')
    const accountMultiplier = log.account_rate_multiplier == null ? 1 : numeric(log.account_rate_multiplier)
    const accountCost = log.account_stats_cost == null
      ? numeric(log.total_cost) * accountMultiplier
      : numeric(log.account_stats_cost)
    const existing = rows.get(key)

    if (existing) {
      existing.requests += 1
      existing.inputTokens += numeric(log.input_tokens)
      existing.cacheReadTokens += numeric(log.cache_read_tokens)
      existing.outputTokens += numeric(log.output_tokens)
      existing.standardCost += numeric(log.total_cost)
      existing.accountCost += accountCost
      existing.revenue += numeric(log.actual_cost)
      if (log.created_at < existing.firstSeen) existing.firstSeen = log.created_at
      if (log.created_at > existing.lastSeen) existing.lastSeen = log.created_at
      continue
    }

    rows.set(key, {
      key,
      requestedModel,
      upstreamModel,
      upstreamResponseModel,
      upstreamModelMismatch,
      modelAuditStatus,
      mappingChain: text(log.model_mapping_chain, requestedModel === upstreamModel ? '未映射' : `${requestedModel} → ${upstreamModel}`),
      provider: classifyModelProvider(upstreamModel),
      accountId,
      accountName: text(log.account?.name, accountId == null ? '已删除账号（历史）' : `账号 #${accountId}`),
      channelId,
      channelName: channelId == null ? '直属账号（无渠道）' : text(channelNames.get(channelId), `渠道 #${channelId}`),
      groupId: log.group_id == null ? null : Number(log.group_id),
      groupName: text(log.group?.name, log.group_id == null ? '未分组' : `分组 #${log.group_id}`),
      inboundEndpoint,
      upstreamEndpoint,
      channelPricing: channelId == null ? null : findChannelPricing(channelById.get(channelId), upstreamModel),
      requests: 1,
      inputTokens: numeric(log.input_tokens),
      cacheReadTokens: numeric(log.cache_read_tokens),
      outputTokens: numeric(log.output_tokens),
      standardCost: numeric(log.total_cost),
      accountCost,
      revenue: numeric(log.actual_cost),
      firstSeen: log.created_at,
      lastSeen: log.created_at,
    })
  }

  return [...rows.values()].sort((left, right) => right.accountCost - left.accountCost || right.requests - left.requests)
}
