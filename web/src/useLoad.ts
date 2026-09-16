import { useCallback, useEffect, useState } from 'react'
import { errText } from './api'

/**
 * Minimal fetch-on-mount helper: gives every page the same loading/error/data
 * triple plus a manual `reload()`. Keeps the pages free of boilerplate.
 */
export function useLoad<T>(loader: () => Promise<T>, deps: readonly unknown[] = []) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    let alive = true
    setLoading(true)
    loader()
      .then((result) => {
        if (!alive) return
        setData(result)
        setError(null)
      })
      .catch((e: unknown) => {
        if (alive) setError(errText(e))
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce])

  return { data, error, loading, reload, setData }
}
