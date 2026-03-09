import { useState, useEffect, useRef, useLayoutEffect } from 'react'
import {
  GetParagraphsBatch, TranslateParagraphs,
  FixTranslation, SavePosition, GetLastPosition, SaveProject,
  GetGlossary, SetGlossaryEntry, DeleteGlossaryEntry,
  onTranslationComplete,
} from '../api'
import { toast } from '../App'

const MIN_FONT      = 12
const MAX_FONT      = 36
const OVERLAY_HIDE  = 4000
const BATCH         = 30   // max paragraphs fetched per page

// ── GlossaryModal ─────────────────────────────────────────────────────────────
// Used for both adding a new entry (termEditable=true) and editing an existing one.
function GlossaryModal({ term: initTerm, initial, termEditable, onSave, onDelete, onClose }) {
  const [term,   setTerm]   = useState(initTerm)
  const [target, setTarget] = useState(initial)
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ width: 360 }} onClick={e => e.stopPropagation()}>
        <h2>{termEditable ? 'Add Glossary Entry' : 'Glossary Entry'}</h2>
        <div className="form-row">
          <label>Source term</label>
          {termEditable
            ? <input className="form-input" value={term} onChange={e => setTerm(e.target.value)}
                placeholder="Source term…" autoFocus />
            : <div className="form-input" style={{ opacity: .72 }}>{term}</div>
          }
        </div>
        <div className="form-row" style={{ marginTop: 12 }}>
          <label>Translation</label>
          <input className="form-input" value={target} dir="auto"
            onChange={e => setTarget(e.target.value)}
            autoFocus={!termEditable}
            placeholder="Enter translation…" />
        </div>
        <div className="modal-actions">
          {!termEditable && initial && (
            <button className="btn danger" onClick={() => onDelete(term)}>Delete</button>
          )}
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" disabled={!term.trim() || !target.trim()}
            onClick={() => onSave(term.trim(), target.trim())}>Save</button>
        </div>
      </div>
    </div>
  )
}

// ── GlossaryListModal ─────────────────────────────────────────────────────────
// Shows all glossary entries with edit / delete per entry, plus an Add button.
function GlossaryListModal({ glossary, onEdit, onAdd, onClose }) {
  const entries = Object.entries(glossary)
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal book-info-modal" onClick={e => e.stopPropagation()}>
        <h2>📖 Glossary ({entries.length})</h2>
        {entries.length === 0 && (
          <p style={{ color: 'var(--fg-muted)', fontSize: 14, margin: '12px 0' }}>
            No entries yet. Select text while reading to add terms.
          </p>
        )}
        {entries.map(([src, tgt]) => (
          <div key={src} className="glossary-list-row">
            <span className="glossary-src">{src}</span>
            <span className="glossary-arrow">→</span>
            <span className="glossary-tgt">{tgt}</span>
            <button className="btn" style={{ padding: '4px 10px', fontSize: 12 }}
              onClick={() => onEdit(src, tgt)}>Edit</button>
          </div>
        ))}
        <div className="modal-actions">
          <button className="btn" onClick={onAdd}>+ Add Entry</button>
          <button className="btn primary" onClick={onClose}>Done</button>
        </div>
      </div>
    </div>
  )
}

export default function Reader({ project, onBack, theme, onToggleTheme }) {
  // ── Page state ───────────────────────────────────────────────────────────
  const [pageStart,      setPageStart]      = useState(0)       // first para index on this page
  const [paragraphs,     setParagraphs]     = useState([])      // fetched batch
  const [nextStart,      setNextStart]      = useState(null)    // first index of next page (null = last page)
  const [history,        setHistory]        = useState([])      // back-stack of pageStart values

  // ── UI state ─────────────────────────────────────────────────────────────
  const [translatingSet, setTranslatingSet] = useState(new Set()) // para indices being translated
  const [isSource,       setIsSource]       = useState(true)
  const [fontSize,       setFontSize]       = useState(18)
  const [showOverlay,    setShowOverlay]    = useState(false)
  const [fittingCount,   setFittingCount]   = useState(BATCH)    // how many paras fully fit on page

  // ── Glossary state ────────────────────────────────────────────────────────
  const [glossary,        setGlossary]        = useState({})
  const [selectionPopup,  setSelectionPopup]  = useState(null) // {term, x, y} | null
  const [glossaryModal,   setGlossaryModal]   = useState(null) // {term, initial, termEditable} | null
  const [showGlossaryList, setShowGlossaryList] = useState(false)

  // ── Refs ─────────────────────────────────────────────────────────────────
  const overlayTimer = useRef(null)
  const clipRef      = useRef(null)   // .reader-text-clip (the overflow boundary)
  const paraRefs     = useRef([])     // one DOM ref per rendered paragraph

  // ── Load last saved position on mount ───────────────────────────────────
  useEffect(() => {
    GetLastPosition(project.path).then(pos => {
      setPageStart(pos.index)
      setIsSource(pos.sourceView)
    }).catch(() => {})
  }, [project.path])

  // ── Fetch batch whenever pageStart or isSource changes ──────────────────
  useEffect(() => {
    let cancelled = false
    GetParagraphsBatch(project.path, pageStart, BATCH, isSource)
      .then(batch => {
        if (cancelled) return
        const items = batch || []
        setParagraphs(items)
        // Mark empty target paragraphs as pending translation
        if (!isSource) {
          setTranslatingSet(new Set(items.filter(p => !p.text).map(p => p.index)))
        } else {
          setTranslatingSet(new Set())
        }
      })
      .catch(e => toast(`Could not load paragraphs: ${e}`, 'error'))
    return () => { cancelled = true }
  }, [project.path, pageStart, isSource])

  // ── Chapter-aware visible slice ───────────────────────────────────────────
  // Find the first chapter-start that is NOT at position 0 on this page.
  // Paragraphs from that point belong to the next chapter and must not be
  // rendered on the current page — even if they would fit visually.
  const chapterCutoff = paragraphs.findIndex((p, i) => i > 0 && p.chapterStart)
  const visibleParagraphs = chapterCutoff > 0
    ? paragraphs.slice(0, chapterCutoff)
    : paragraphs

  // ── After-render: measure which paragraph first overflows the container ──
  // visibleParagraphs is already chapter-clamped, so we only need to check
  // visual overflow here. The default nextStart is the chapter boundary
  // (if any); visual overflow can only make it earlier.
  useLayoutEffect(() => {
    // Default: chapter boundary (null if no boundary in this batch)
    const defaultNext = chapterCutoff > 0 ? pageStart + chapterCutoff : null

    if (!clipRef.current || visibleParagraphs.length === 0) {
      setNextStart(defaultNext)
      setFittingCount(visibleParagraphs.length)
      return
    }

    // clipRef is the inner .reader-text-clip div whose border IS the clip line.
    // Its bottom == container.bottom - paddingBottom (padding is on the outer div).
    // Clamp to window.innerHeight as a safety net for Android WebView.
    const clipBottom = Math.min(
      clipRef.current.getBoundingClientRect().bottom,
      window.innerHeight
    )

    let next    = defaultNext
    let fitting = visibleParagraphs.length
    for (let i = 0; i < visibleParagraphs.length; i++) {
      const el = paraRefs.current[i]
      if (!el) continue
      if (el.getBoundingClientRect().bottom > clipBottom) {
        // Visual overflow — always advance at least 1 paragraph per page
        next    = pageStart + Math.max(1, i)
        fitting = Math.max(1, i)
        break
      }
    }
    setNextStart(next)
    setFittingCount(fitting)
  }, [paragraphs, fontSize])

  // ── Listen for translation:complete events ───────────────────────────────
  useEffect(() => {
    function onTranslated(ev) {
      if (ev.projectPath !== project.path) return
      setParagraphs(ps => ps.map(p =>
        p.index === ev.index ? { ...p, text: ev.text } : p
      ))
      setTranslatingSet(s => { const n = new Set(s); n.delete(ev.index); return n })
    }
    return onTranslationComplete(onTranslated)
  }, [project.path])

  // ── Trigger translation when viewing target mode ─────────────────────────
  useEffect(() => {
    if (!isSource) {
      TranslateParagraphs(project.path, pageStart).catch(() => {})
    }
  }, [isSource, pageStart, project.path])

  // ── Save position on every page/view change ──────────────────────────────
  useEffect(() => {
    SavePosition(project.path, pageStart, isSource).catch(() => {})
  }, [project.path, pageStart, isSource])

  // ── Load glossary on project open ────────────────────────────────────────
  useEffect(() => {
    GetGlossary(project.path).then(g => setGlossary(g || {})).catch(() => {})
  }, [project.path])

  // ── Overlay auto-hide ────────────────────────────────────────────────────
  function showOverlayTemporarily() {
    clearTimeout(overlayTimer.current)
    setShowOverlay(true)
    overlayTimer.current = setTimeout(() => setShowOverlay(false), OVERLAY_HIDE)
  }
  function toggleOverlay() {
    if (showOverlay) {
      clearTimeout(overlayTimer.current)
      setShowOverlay(false)
    } else {
      showOverlayTemporarily()
    }
  }
  useEffect(() => () => clearTimeout(overlayTimer.current), [])

  // ── RTL detection (based on first paragraph's direction) ─────────────────
  const isRtl = paragraphs[0]?.direction === 'rtl'

  // ── Navigation ────────────────────────────────────────────────────────────
  function navigate(delta) {
    if (delta > 0) {
      if (nextStart === null) return   // already on last page
      setHistory(h => [...h, pageStart])
      setPageStart(nextStart)
    } else {
      if (history.length === 0) return
      const prev = history[history.length - 1]
      setHistory(h => h.slice(0, -1))
      setPageStart(prev)
    }
  }

  // ── Tap handler — RTL swaps left/right semantics ─────────────────────────
  function handleTap(e) {
    // If the user just finished selecting text, clear the selection and show the
    // glossary popup instead of navigating.
    const sel = window.getSelection()
    if (sel && !sel.isCollapsed) {
      sel.removeAllRanges()
      setSelectionPopup(null)
      return
    }
    setSelectionPopup(null)
    const rect = e.currentTarget.getBoundingClientRect()
    const x    = e.clientX - rect.left
    const w    = rect.width
    if      (x < w * 0.28) isRtl ? navigate(+1) : navigate(-1)
    else if (x > w * 0.72) isRtl ? navigate(-1) : navigate(+1)
    else                   toggleOverlay()
  }

  // ── Font size ─────────────────────────────────────────────────────────────
  function adjustFont(delta) {
    setFontSize(f => Math.min(MAX_FONT, Math.max(MIN_FONT, f + delta)))
    showOverlayTemporarily()
  }

  // ── Fix translation (always targets first paragraph on page) ─────────────
  const fixIndex     = paragraphs[0]?.index ?? 0
  const isTranslating = translatingSet.size > 0

  async function handleFix() {
    setTranslatingSet(s => new Set([...s, fixIndex]))
    try {
      await FixTranslation(project.path, fixIndex)
    } catch (e) {
      toast(`Fix failed: ${e}`, 'error')
      setTranslatingSet(s => { const n = new Set(s); n.delete(fixIndex); return n })
    }
  }

  // ── Save project ──────────────────────────────────────────────────────────
  async function handleSave() {
    try {
      await SaveProject(project.path)
      toast('Saved', 'success')
    } catch (e) {
      toast(`Save failed: ${e}`, 'error')
    }
  }

  // ── Glossary ──────────────────────────────────────────────────────────────

  // Called on mouseup inside the text container — shows popup if text selected.
  function handleTextSelection() {
    const sel = window.getSelection()
    if (!sel || sel.isCollapsed || !sel.rangeCount) { setSelectionPopup(null); return }
    const term = sel.toString().trim()
    if (!term) { setSelectionPopup(null); return }
    const rect = sel.getRangeAt(0).getBoundingClientRect()
    setSelectionPopup({ term, x: (rect.left + rect.right) / 2, y: rect.top })
  }

  function openGlossaryModal(term) {
    window.getSelection()?.removeAllRanges()
    setSelectionPopup(null)
    setGlossaryModal({ term, initial: glossary[term] || '', termEditable: false })
  }

  async function saveGlossaryEntry(term, targetTerm) {
    try {
      await SetGlossaryEntry(project.path, term, targetTerm)
      setGlossary(g => ({ ...g, [term]: targetTerm }))
      setGlossaryModal(null)
      toast('Glossary updated', 'success')
    } catch (e) { toast(`Glossary save failed: ${e}`, 'error') }
  }

  async function deleteGlossaryEntry(term) {
    try {
      await DeleteGlossaryEntry(project.path, term)
      setGlossary(g => { const n = { ...g }; delete n[term]; return n })
      setGlossaryModal(null)
      toast('Glossary entry removed', 'success')
    } catch (e) { toast(`Glossary delete failed: ${e}`, 'error') }
  }

  // ── Progress ──────────────────────────────────────────────────────────────
  const total    = paragraphs[0]?.total ?? project.total ?? 1
  const lastIdx  = nextStart !== null ? nextStart - 1 : pageStart + visibleParagraphs.length - 1
  const percent  = total > 0 ? Math.round(((pageStart + 1) / total) * 100) : 0

  return (
    <div className="reader-page">

      {/* ── Top bar ── */}
      <div className="reader-topbar">
        <button className="btn" style={{ padding: '6px 12px', fontSize: 13 }} onClick={onBack}>
          ← Library
        </button>
        <div className="reader-title">
          {project.title || project.name}
        </div>
        <div className="reader-view-toggle">
          <button
            className={`reader-view-btn ${isSource ? 'active' : ''}`}
            onClick={() => setIsSource(true)}
          >Source</button>
          <button
            className={`reader-view-btn ${!isSource ? 'active' : ''}`}
            onClick={() => setIsSource(false)}
          >Trans.</button>
        </div>
        <button className="theme-btn" onClick={onToggleTheme} title="Toggle theme">
          {theme === 'dark' ? '☀️' : '🌙'}
        </button>
      </div>

      {/* ── Tap area ── */}
      <div className="reader-tap-area" onClick={handleTap}>

        {/* Direction hints flip for RTL */}
        <div className="reader-zone-hint left">{isRtl ? '›' : '‹'}</div>
        <div className="reader-zone-hint right">{isRtl ? '‹' : '›'}</div>

        {/* Text container — padding provides symmetric top/bottom margin.
            The inner .reader-text-clip div is where overflow:hidden lives,
            so the clip line is always exactly at the padding boundary.     */}
        <div
          className={`reader-text-container${isRtl ? ' rtl' : ''}`}
          onMouseUp={handleTextSelection}
        >
          <div className="reader-text-clip" ref={clipRef}>
            {/* Subtle chapter separator when this page opens a new chapter
                (but not at the very start of the book, index 0) */}
            {paragraphs[0]?.chapterStart && paragraphs[0]?.index > 0 && (
              <div className="chapter-separator" />
            )}
            {visibleParagraphs.map((p, i) => (
              <p
                key={p.index}
                ref={el => { paraRefs.current[i] = el }}
                className={`reader-text ${isRtl ? 'rtl' : ''} ${!p.text ? 'empty' : ''}`}
                style={{ fontSize: `${fontSize}px`, visibility: i < fittingCount ? 'visible' : 'hidden' }}
              >
                {translatingSet.has(p.index)
                  ? <span className="spinner" style={{ margin: '6px 0', display: 'inline-block' }} />
                  : (p.text || (isSource ? 'No content' : 'Translating…'))}
              </p>
            ))}
          </div>
        </div>

        {/* Progress bar */}
        <div style={{
          position: 'absolute', bottom: 0, left: 0, right: 0,
          height: 2,
          background: 'var(--border)',
        }}>
          <div style={{
            width: `${percent}%`,
            height: '100%',
            background: 'var(--accent)',
            transition: 'width .4s ease',
          }} />
        </div>

        {/* Overlay */}
        {showOverlay && (
          <div className="reader-overlay" onClick={e => e.stopPropagation()}>
            <div className="overlay-font-btns">
              <button className="font-btn minus" onClick={() => adjustFont(-2)}>A−</button>
              <button className="font-btn"       onClick={() => adjustFont(+2)}>A+</button>
            </div>
            <div className="overlay-divider" />
            <div className="overlay-actions">
              {!isSource && (
                <button className="btn" onClick={handleFix} disabled={isTranslating}>
                  🔧 Fix
                </button>
              )}
              <button className="btn" onClick={handleSave}>💾 Save</button>
              <button className="btn" onClick={() => { setShowOverlay(false); setShowGlossaryList(true) }}
                title="Manage glossary">
                📖{Object.keys(glossary).length > 0 ? ` ${Object.keys(glossary).length}` : ''}
              </button>
            </div>
            <div className="overlay-divider" />
            <div className="overlay-progress">
              {pageStart + 1}–{lastIdx + 1} / {total} ({percent}%)
            </div>
          </div>
        )}
      </div>

      {/* ── Selection → Glossary popup ── */}
      {selectionPopup && (
        <div className="glossary-popup" style={{ left: selectionPopup.x, top: selectionPopup.y }}>
          <button className="btn" style={{ fontSize: 12, padding: '4px 10px', gap: 4 }}
            onClick={() => openGlossaryModal(selectionPopup.term)}>
            📖 {glossary[selectionPopup.term] ? 'Edit' : 'Add'}
          </button>
        </div>
      )}

      {/* ── Glossary entry modal ── */}
      {glossaryModal && (
        <GlossaryModal
          term={glossaryModal.term}
          initial={glossaryModal.initial}
          termEditable={glossaryModal.termEditable}
          onSave={saveGlossaryEntry}
          onDelete={deleteGlossaryEntry}
          onClose={() => setGlossaryModal(null)}
        />
      )}

      {/* ── Glossary list modal ── */}
      {showGlossaryList && (
        <GlossaryListModal
          glossary={glossary}
          onEdit={(src, tgt) => { setShowGlossaryList(false); setGlossaryModal({ term: src, initial: tgt, termEditable: false }) }}
          onAdd={() => { setShowGlossaryList(false); setGlossaryModal({ term: '', initial: '', termEditable: true }) }}
          onClose={() => setShowGlossaryList(false)}
        />
      )}
    </div>
  )
}
