import { useEffect, useState } from 'react'
import type { NodePerfSnapshot } from './types'

// usePerfStream subscribes to the Server-Sent Events endpoint at `url` and
// returns the most recently received []NodePerfSnapshot. Returns null until
// the first event is received. The EventSource reconnects automatically on
// network errors.
export function usePerfStream(url: string): NodePerfSnapshot[] | null {
  const [data, setData] = useState<NodePerfSnapshot[] | null>(null)

  useEffect(() => {
    const source = new EventSource(url)

    source.onmessage = (event: MessageEvent<string>) => {
      try {
        const parsed: unknown = JSON.parse(event.data)
        if (Array.isArray(parsed)) {
          setData(parsed as NodePerfSnapshot[])
        }
      } catch {
        // Ignore malformed events; the stream will recover on the next tick.
      }
    }

    source.onerror = () => {
      // EventSource reconnects automatically; nothing to do here.
    }

    return () => {
      source.close()
    }
  }, [url])

  return data
}
