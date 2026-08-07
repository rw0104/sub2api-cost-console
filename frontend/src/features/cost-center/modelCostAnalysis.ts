import type { Account, ModelStat } from '@/types'
import { describeUpstreamOrigin } from './upstreamProvider'

export type ModelCostSource = 'requested' | 'upstream' | 'mapping'

export interface ModelCostRow extends ModelStat {
  provider: string
  standardCost: number
  accountCost: number
  revenue: number
  grossProfit: number
  grossMargin: number | null
  accountCostEstimated: boolean
  pricingMissing: boolean
}

export interface ModelCostSummary {
  modelCount: number
  standardCost: number
  accountCost: number
  revenue: number
  grossProfit: number
  grossMargin: number | null
  estimatedModelCount: number
  missingPricingCount: number
}

function finiteNumber(value: unknown): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function roundedMoney(value: number): number {
  return Math.round(value * 1_000_000_000_000) / 1_000_000_000_000
}

export function classifyModelProvider(model: string): string {
  const normalized = String(model || '').toLowerCase()
  if (normalized.includes('deepseek')) return 'DeepSeek'
  if (/(^|[/_-])(gpt|o[134])([/_-]|\d|$)/.test(normalized)) return 'OpenAI'
  if (normalized.includes('claude')) return 'Anthropic'
  if (normalized.includes('gemini')) return 'Google'
  if (normalized.includes('grok')) return 'xAI'
  if (normalized.includes('mistral') || normalized.includes('mixtral') || normalized.includes('codestral')) return 'Mistral'
  if (normalized.includes('llama')) return 'Meta / Llama'
  if (normalized.includes('qwen') || normalized.includes('qwq')) return 'Alibaba / Qwen'
  if (normalized.includes('glm') || normalized.includes('chatglm')) return 'Z.ai / GLM'
  if (normalized.includes('moonshot') || normalized.includes('kimi')) return 'Moonshot / Kimi'
  if (normalized.includes('minimax')) return 'MiniMax'
  if (/(^|[/_-])command-[ar]([/_-]|$)/.test(normalized)) return 'Cohere'
  if (normalized.includes('sonar')) return 'Perplexity'
  return '其他/中转'
}

export function buildModelCostRows(models: ModelStat[]): ModelCostRow[] {
  return models
    .map((model) => {
      const standardCost = finiteNumber(model.cost)
      const hasAccountCost = typeof model.account_cost === 'number' && Number.isFinite(model.account_cost)
      const accountCost = hasAccountCost ? finiteNumber(model.account_cost) : standardCost
      const revenue = finiteNumber(model.actual_cost)
      const grossProfit = revenue - accountCost
      const totalTokens = finiteNumber(model.total_tokens)

      return {
        ...model,
        requests: finiteNumber(model.requests),
        input_tokens: finiteNumber(model.input_tokens),
        output_tokens: finiteNumber(model.output_tokens),
        cache_creation_tokens: finiteNumber(model.cache_creation_tokens),
        cache_read_tokens: finiteNumber(model.cache_read_tokens),
        total_tokens: totalTokens,
        cost: standardCost,
        actual_cost: revenue,
        provider: classifyModelProvider(model.model),
        standardCost,
        accountCost,
        revenue,
        grossProfit,
        grossMargin: revenue > 0 ? grossProfit / revenue : null,
        accountCostEstimated: !hasAccountCost,
        pricingMissing: totalTokens > 0 && standardCost === 0 && accountCost === 0 && revenue === 0,
      }
    })
    .sort((left, right) => right.accountCost - left.accountCost || right.requests - left.requests)
}

export function summarizeModelCosts(rows: ModelCostRow[]): ModelCostSummary {
  const summary = rows.reduce((result, row) => {
    result.standardCost += row.standardCost
    result.accountCost += row.accountCost
    result.revenue += row.revenue
    result.grossProfit += row.grossProfit
    if (row.accountCostEstimated) result.estimatedModelCount += 1
    if (row.pricingMissing) result.missingPricingCount += 1
    return result
  }, {
    modelCount: rows.length,
    standardCost: 0,
    accountCost: 0,
    revenue: 0,
    grossProfit: 0,
    grossMargin: null as number | null,
    estimatedModelCount: 0,
    missingPricingCount: 0,
  })

  summary.standardCost = roundedMoney(summary.standardCost)
  summary.accountCost = roundedMoney(summary.accountCost)
  summary.revenue = roundedMoney(summary.revenue)
  summary.grossProfit = roundedMoney(summary.grossProfit)
  summary.grossMargin = summary.revenue > 0 ? summary.grossProfit / summary.revenue : null
  return summary
}

export function describeCostAccountOrigin(account: Account): string {
  return describeUpstreamOrigin(account)
}
