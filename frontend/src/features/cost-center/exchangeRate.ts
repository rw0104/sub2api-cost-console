import { CNY_PER_USD } from './model'

export type ExchangeRateSource = 'network' | 'cache' | 'fallback'

export interface UsdCnyExchangeRate {
  rate: number
  rateDate: string | null
  fetchedAt: string | null
  source: ExchangeRateSource
}

interface FrankfurterRateResponse {
  date?: unknown
  base?: unknown
  quote?: unknown
  rate?: unknown
  rates?: Record<string, unknown>
}

const ENDPOINTS = [
  'https://api.frankfurter.dev/v2/rate/USD/CNY',
  'https://api.frankfurter.app/latest?from=USD&to=CNY',
]
const CACHE_KEY = 'sub2api.cost-center.usd-cny-rate.v1'
const CACHE_TTL_MS = 12 * 60 * 60 * 1000
const REQUEST_TIMEOUT_MS = 8_000

function isValidRate(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 4 && value <= 10
}

function parseCachedRate(storage: Storage | null, now: number): UsdCnyExchangeRate | null {
  if (!storage) return null
  try {
    const cached = JSON.parse(storage.getItem(CACHE_KEY) || '') as Partial<UsdCnyExchangeRate>
    const fetchedAt = typeof cached.fetchedAt === 'string' ? Date.parse(cached.fetchedAt) : Number.NaN
    if (!isValidRate(cached.rate) || !Number.isFinite(fetchedAt) || now - fetchedAt > CACHE_TTL_MS) return null
    return {
      rate: cached.rate,
      rateDate: typeof cached.rateDate === 'string' ? cached.rateDate : null,
      fetchedAt: cached.fetchedAt ?? null,
      source: 'cache',
    }
  } catch {
    return null
  }
}

function persistRate(storage: Storage | null, value: UsdCnyExchangeRate): void {
  if (!storage) return
  try {
    storage.setItem(CACHE_KEY, JSON.stringify(value))
  } catch {
    // Storage can be disabled; the in-memory value remains usable for this session.
  }
}

export async function loadUsdCnyExchangeRate(options: {
  fetcher?: typeof fetch
  storage?: Storage | null
  now?: number
} = {}): Promise<UsdCnyExchangeRate> {
  const fetcher = options.fetcher ?? fetch
  const storage = options.storage === undefined
    ? (typeof window === 'undefined' ? null : window.localStorage)
    : options.storage
  const now = options.now ?? Date.now()
  const cached = parseCachedRate(storage, now)
  if (cached) return cached

  try {
    let lastError: unknown = null
    for (const endpoint of ENDPOINTS) {
      const controller = new AbortController()
      const timeout = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS)
      try {
        const response = await fetcher(endpoint, {
          headers: { Accept: 'application/json' },
          signal: controller.signal,
        })
        if (!response.ok) throw new Error(`exchange-rate request returned ${response.status}`)
        const payload = await response.json() as FrankfurterRateResponse
        const rate = payload.rate ?? payload.rates?.CNY
        if (payload.base !== 'USD' || !isValidRate(rate)) {
          throw new Error('exchange-rate response is invalid')
        }
        const result: UsdCnyExchangeRate = {
          rate,
          rateDate: typeof payload.date === 'string' ? payload.date : null,
          fetchedAt: new Date(now).toISOString(),
          source: 'network',
        }
        persistRate(storage, result)
        return result
      } catch (error) {
        lastError = error
      } finally {
        clearTimeout(timeout)
      }
    }
    throw lastError instanceof Error ? lastError : new Error('exchange-rate request failed')
  } catch (error) {
    console.warn('[cost-center] USD/CNY reference rate unavailable; using fallback', error)
    return {
      rate: CNY_PER_USD,
      rateDate: null,
      fetchedAt: null,
      source: 'fallback',
    }
  }
}
