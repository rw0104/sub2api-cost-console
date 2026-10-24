import { describe, expect, it, vi } from 'vitest'
import { loadUsdCnyExchangeRate } from '../exchangeRate'

function memoryStorage(): Storage {
  const values = new Map<string, string>()
  return {
    get length() { return values.size },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key) },
    setItem: (key, value) => { values.set(key, value) },
  }
}

describe('USD/CNY exchange-rate synchronization', () => {
  it('uses and caches a valid network reference rate', async () => {
    const storage = memoryStorage()
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      date: '2026-08-05',
      base: 'USD',
      quote: 'CNY',
      rate: 7.1842,
    }), { status: 200 }))

    const network = await loadUsdCnyExchangeRate({ fetcher, storage, now: Date.UTC(2026, 7, 6) })
    const cached = await loadUsdCnyExchangeRate({
      fetcher: vi.fn().mockRejectedValue(new Error('network should not be called')),
      storage,
      now: Date.UTC(2026, 7, 6, 1),
    })

    expect(network).toMatchObject({ rate: 7.1842, rateDate: '2026-08-05', source: 'network' })
    expect(cached).toMatchObject({ rate: 7.1842, rateDate: '2026-08-05', source: 'cache' })
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it('falls back to the configured baseline when synchronization fails', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)

    await expect(loadUsdCnyExchangeRate({
      fetcher: vi.fn().mockRejectedValue(new Error('offline')),
      storage: memoryStorage(),
    })).resolves.toMatchObject({ rate: 7.2, source: 'fallback' })
  })

  it('tries the compact endpoint when the primary endpoint is unavailable', async () => {
    const storage = memoryStorage()
    const fetcher = vi.fn()
      .mockRejectedValueOnce(new Error('primary unavailable'))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        amount: 1,
        base: 'USD',
        date: '2026-08-06',
        rates: { CNY: 6.7491 },
      }), { status: 200 }))

    await expect(loadUsdCnyExchangeRate({ fetcher, storage })).resolves.toMatchObject({
      rate: 6.7491,
      source: 'network',
    })
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
})
