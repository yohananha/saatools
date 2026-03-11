import { useState, useEffect, useRef, useCallback } from 'react'
import { GetLog } from '../api'
import { toast } from '../App'

/**
 * Log viewer.
 * - standalone (default): full page with header
 * - inline: compact embed for use inside Settings — no page header, fixed-height list
 */
export default function Log({ inline = false }) {
  const [lines,   setLines]   = useState([])
  const [loading, setLoading] = useState(false)
  const bottomRef = useRef(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const l = await GetLog()
      setLines(l || [])
    } catch (e) {
      toast(`Could not load log: ${e}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [])

  // auto-refresh every 3 s while visible
  useEffect(() => {
    refresh()
    const id = setInterval(refresh, 3000)
    return () => clearInterval(id)
  }, [refresh])

  // scroll to bottom on new lines
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [lines])

  const listContent = (
    <>
      {lines.length === 0 && (
        <div style={{ color: 'var(--fg-subtle)', padding: 16 }}>No log entries yet.</div>
      )}
      {lines.map((line, i) => (
        <div key={i} className="log-entry">{line}</div>
      ))}
      <div ref={bottomRef} />
    </>
  )

  if (inline) {
    return (
      <div>
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
          <button className="btn" onClick={refresh} disabled={loading} style={{ fontSize: 12 }}>
            {loading
              ? <span className="spinner" style={{ width: 12, height: 12, borderWidth: 2 }} />
              : '🔄'} Refresh
          </button>
        </div>
        <div className="log-list" style={{ maxHeight: 220, overflowY: 'auto' }}>
          {listContent}
        </div>
      </div>
    )
  }

  return (
    <div className="log-page">
      <div className="page-header">
        <span className="page-title">Log</span>
        <button className="btn" onClick={refresh} disabled={loading}>
          {loading ? <span className="spinner" style={{ width: 14, height: 14, borderWidth: 2 }} /> : '🔄'}
          Refresh
        </button>
      </div>
      <div className="log-list">
        {listContent}
      </div>
    </div>
  )
}
