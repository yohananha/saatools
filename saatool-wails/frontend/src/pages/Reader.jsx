import { useState, useEffect, useRef, useLayoutEffect, useMemo } from 'react'
import {
  GetParagraphsBatch, TranslateParagraphs, SetActiveProject,
  FixTranslation, SavePosition, GetLastPosition, SaveProject,
  GetGlossary, SetGlossaryEntry, DeleteGlossaryEntry,
  GetBookmarks, AddBookmark, DeleteBookmark,
  onTranslationComplete, ExportProject, ExportProjectEPUB,
} from '../api'
import { toast } from '../App'

const langCode = lang => (lang || '?').slice(0, 2).toUpperCase()

const MIN_FONT      = 12
const MAX_FONT      = 36
const OVERLAY_HIDE  = 4000
const BATCH         = 30

// ── GlossaryModal ─────────────────────────────────────────────────────────────
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
                placeholder="Source term…" autoFocus maxLength={50} />
            : <div className="form-input" style={{ opacity: .72 }}>{term}</div>
          }
        </div>
        <div className="form-row" style={{ marginTop: 12 }}>
          <label>Translation</label>
          <input className="form-input" value={target} dir="auto"
            onChange={e => setTarget(e.target.value)}
            autoFocus={!termEditable}
            placeholder="Enter translation…" maxLength={50} />
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

// ── BookmarkAddModal ───────────────────────────────────────────────────────────
function BookmarkAddModal({ index, existing, onSave, onDelete, onClose }) {
  const [note, setNote] = useState(existing?.note || '')
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" style={{ width: 340 }} onClick={e => e.stopPropagation()}>
        <h2>🔖 {existing ? 'Edit Bookmark' : 'Add Bookmark'}</h2>
        <div style={{ fontSize: 13, color: 'var(--fg-subtle)', marginBottom: 12 }}>
          Paragraph {index + 1}
        </div>
        <div className="form-row">
          <label>Note (optional)</label>
          <textarea
            className="form-input form-textarea"
            rows={3}
            maxLength={1000}
            value={note}
            onChange={e => setNote(e.target.value)}
            placeholder="Add a note…"
            autoFocus
          />
        </div>
        <div className="modal-actions">
          {existing && (
            <button className="btn danger" onClick={() => onDelete(index)}>Remove</button>
          )}
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" onClick={() => onSave(index, note)}>
            {existing ? 'Update' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  )
}

// ── BookmarkListModal ──────────────────────────────────────────────────────────
function BookmarkListModal({ bookmarks, paragraphTexts, onNavigate, onEdit, onDelete, onClose }) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal book-info-modal" onClick={e => e.stopPropagation()}>
        <h2>🔖 Bookmarks ({bookmarks.length})</h2>
        {bookmarks.length === 0 && (
          <p style={{ color: 'var(--fg-muted)', fontSize: 14, margin: '12px 0' }}>
            No bookmarks yet. Long-press or use the overlay to add one.
          </p>
        )}
        {[...bookmarks].sort((a, b) => a.index - b.index).map(b => (
          <div key={b.index} className="bookmark-list-row">
            <div className="bookmark-list-left" onClick={() => onNavigate(b.index)}>
              <span className="bookmark-para-num">¶{b.index + 1}</span>
              {b.note && <span className="bookmark-note">{b.note}</span>}
              {!b.note && paragraphTexts[b.index] && (
                <span className="bookmark-preview">{paragraphTexts[b.index].slice(0, 60)}…</span>
              )}
            </div>
            <div style={{ display: 'flex', gap: 4, flexShrink: 0 }}>
              <button className="btn" style={{ padding: '4px 10px', fontSize: 12 }}
                onClick={() => onEdit(b)}>✏</button>
              <button className="btn" style={{ padding: '4px 10px', fontSize: 12 }}
                onClick={() => onDelete(b.index)}>✕</button>
            </div>
          </div>
        ))}
        <div className="modal-actions">
          <button className="btn primary" onClick={onClose}>Done</button>
        </div>
      </div>
    </div>
  )
}

// ── GlossaryListModal ──────────────────────────────────────────────────────────
function GlossaryListModal({ glossary, onAdd, onEdit, onClose }) {
  const entries = Object.entries(glossary).sort(([a], [b]) => a.localeCompare(b))
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal book-info-modal" onClick={e => e.stopPropagation()}>
        <h2>📖 Glossary ({entries.length})</h2>
        {entries.length === 0 && (
          <p style={{ color: 'var(--fg-muted)', fontSize: 14, margin: '12px 0' }}>
            No entries yet. Tap <strong>+ Add</strong> or select text in the reader.
          </p>
        )}
        {entries.map(([term, trans]) => (
          <div key={term} className="bookmark-list-row">
            <div className="bookmark-list-left" style={{ cursor: 'pointer' }} onClick={() => onEdit(term, trans)}>
              <span className="bookmark-para-num">{term}</span>
              {trans && <span className="bookmark-note">{trans}</span>}
            </div>
            <button className="btn" style={{ padding: '4px 10px', fontSize: 12, flexShrink: 0 }}
              onClick={() => onEdit(term, trans)}>✏</button>
          </div>
        ))}
        <div className="modal-actions">
          <button className="btn" onClick={onClose}>Close</button>
          <button className="btn primary" onClick={onAdd}>+ Add</button>
        </div>
      </div>
    </div>
  )
}

// ── Returns character offset within `el` at viewport y coordinate ─────────────
// Uses caretRangeFromPoint (Chrome/WebView) with caretPositionFromPoint fallback.
function caretOffsetAtY(el, y) {
  const x = el.getBoundingClientRect().left + 4
  if (document.caretRangeFromPoint) {
    const range = document.caretRangeFromPoint(x, y - 1)
    if (range?.startContainer?.nodeType === Node.TEXT_NODE && el.contains(range.startContainer))
      return range.startOffset
  } else if (document.caretPositionFromPoint) {
    const pos = document.caretPositionFromPoint(x, y - 1)
    if (pos && el.contains(pos.offsetNode)) return pos.offset
  }
  return null
}

// ─────────────────────────────────────────────────────────────────────────────
export default function Reader({ project, onBack, theme, onToggleTheme }) {

  // ── Page position state ───────────────────────────────────────────────────
  // {para, offset}: para = paragraph index; offset = char offset within that para.
  // offset > 0 means this page starts mid-paragraph (continuous text flow).
  const [pageStart,  setPageStart]  = useState({ para: 0, offset: 0 })
  const [paragraphs, setParagraphs] = useState([])
  const [nextStart,  setNextStart]  = useState(null)   // null = last page
  const [history,    setHistory]    = useState([])
  const [fadeKey,    setFadeKey]    = useState(0)

  // Ref always pointing at current pageStart so layout effect can read it
  // without being listed as a dependency (avoids stale-measurement cycle).
  const pageStartRef = useRef(pageStart)
  pageStartRef.current = pageStart

  // Blocks navigation while a page fetch is in flight — set synchronously in
  // navigate() so a double-tap in the same frame is rejected immediately.
  const navigating = useRef(false)

  // ── UI state ─────────────────────────────────────────────────────────────
  const [translatingSet, setTranslatingSet] = useState(new Set())
  const [fixInProgressIndex, setFixInProgressIndex] = useState(null) // only set when user tapped Fix (not when para is just untranslated)
  const [isSource,       setIsSource]       = useState(true)
  const [fontSize,       setFontSize]       = useState(() => {
    const saved = parseInt(localStorage.getItem('babelreader.fontSize'), 10)
    return (saved >= MIN_FONT && saved <= MAX_FONT) ? saved : 18
  })
  const [showOverlay,    setShowOverlay]    = useState(false)
  const [showFontPanel,  setShowFontPanel]  = useState(false)
  const [fittingCount,   setFittingCount]   = useState(BATCH)
  const [keepScreenOn,   setKeepScreenOn]   = useState(
    () => localStorage.getItem('babelreader.keepScreenOn') !== 'false'
  )
  const [showExportReminder, setShowExportReminder] = useState(false)
  const [bookDone,       setBookDone]       = useState(
    () => (project.total ?? 0) > 0 && (project.translated ?? 0) >= (project.total ?? 0)
  )
  const translatedInSession = useRef(0)
  const [translatedCount,   setTranslatedCount]   = useState(project.translated ?? 0)

  const exportReminderKey = useMemo(
    () => `exportReminded_${encodeURIComponent(project.path)}`,
    [project.path],
  )

  // ── Glossary state ────────────────────────────────────────────────────────
  const [glossary,        setGlossary]        = useState({})
  const [selectionPopup,  setSelectionPopup]  = useState(null)
  const [glossaryModal,   setGlossaryModal]   = useState(null)

  // ── Bookmark state ────────────────────────────────────────────────────────
  const [bookmarks,        setBookmarks]        = useState([])
  const [bookmarkModal,    setBookmarkModal]    = useState(null)
  const [showBookmarkList, setShowBookmarkList] = useState(false)
  const [showGlossaryList, setShowGlossaryList] = useState(false)

  // ── Refs ─────────────────────────────────────────────────────────────────
  const overlayTimer = useRef(null)
  const clipRef      = useRef(null)
  const paraRefs     = useRef([])

  // ── Set active project so only this book can run translation; on unmount clear and persist .spz ─
  useEffect(() => {
    SetActiveProject(project.path)
    return () => {
      SetActiveProject('')
      // Persist project to .spz when leaving so translation progress and position are saved
      SaveProject(project.path).catch(() => {})
    }
  }, [project.path])

  // ── Reset session counters when project changes ───────────────────────────
  useEffect(() => {
    translatedInSession.current = 0
    setTranslatedCount(project.translated ?? 0)
  }, [project.path, project.translated])

  // ── Load last saved position on mount ────────────────────────────────────
  useEffect(() => {
    let cancelled = false
    GetLastPosition(project.path).then(pos => {
      if (cancelled) return
      setPageStart({ para: pos.index, offset: 0 })
      setIsSource(pos.sourceView)
    }).catch(() => {})
    return () => { cancelled = true }
  }, [project.path])

  // ── Fetch paragraph batch whenever page or view changes ──────────────────
  // navigating.current is set true here and cleared in .finally() so that
  // the navigation guard is active for the entire duration of the fetch,
  // regardless of whether navigate() or a view-toggle triggered the fetch.
  useEffect(() => {
    let cancelled = false
    navigating.current = true
    GetParagraphsBatch(project.path, pageStart.para, BATCH, isSource)
      .then(batch => {
        if (cancelled) return
        const items = batch || []
        setParagraphs(items)
        setFadeKey(k => k + 1)
        setTranslatingSet(
          !isSource ? new Set(items.filter(p => !p.text).map(p => p.index)) : new Set()
        )
      })
      .catch(e => toast(`Could not load paragraphs: ${e}`, 'error'))
      .finally(() => { if (!cancelled) navigating.current = false })
    return () => { cancelled = true }
  }, [project.path, pageStart, isSource])

  // ── Chapter-aware visible slice ───────────────────────────────────────────
  // Stop at the first chapter boundary that is not at position 0 on this page.
  const chapterCutoff    = paragraphs.findIndex((p, i) => i > 0 && p.chapterStart)
  const visibleParagraphs = chapterCutoff > 0 ? paragraphs.slice(0, chapterCutoff) : paragraphs

  // ── Measure DOM after render; compute nextStart ───────────────────────────
  // IMPORTANT: pageStart is NOT in the dep array — it is read via pageStartRef.
  // Having it in deps caused the layout effect to run with stale (old-page)
  // paragraphs whenever pageStart changed, always computing nextStart=null
  // and breaking forward navigation.
  useLayoutEffect(() => {
    const ps = pageStartRef.current

    // Chapter boundary: next page starts at the first para of the next chapter.
    // Use paragraphs[chapterCutoff].index (absolute), not pageStart.para + offset.
    const defaultNext = chapterCutoff > 0 && paragraphs[chapterCutoff]
      ? { para: paragraphs[chapterCutoff].index, offset: 0 }
      : null

    if (!clipRef.current || visibleParagraphs.length === 0) {
      setNextStart(defaultNext)
      setFittingCount(visibleParagraphs.length)
      return
    }

    const clipBottom = Math.min(
      clipRef.current.getBoundingClientRect().bottom,
      window.innerHeight
    )

    let next    = defaultNext
    let fitting = visibleParagraphs.length

    for (let i = 0; i < visibleParagraphs.length; i++) {
      const el = paraRefs.current[i]
      if (!el) continue
      const rect = el.getBoundingClientRect()
      if (rect.bottom <= clipBottom) continue   // fully visible — keep going

      fitting = Math.max(1, i)

      if (rect.top >= clipBottom) {
        // Whole paragraph below fold — push it entirely to next page
        next = { para: visibleParagraphs[i].index, offset: 0 }
      } else {
        // Paragraph straddles fold — split at the character boundary
        const charOffset = caretOffsetAtY(el, clipBottom)
        if (charOffset !== null && charOffset > 0) {
          // Add back the slice offset that was applied to the first paragraph
          const baseOffset = i === 0 ? ps.offset : 0
          next = { para: visibleParagraphs[i].index, offset: baseOffset + charOffset }
        } else {
          next = { para: visibleParagraphs[i].index, offset: 0 }
        }
      }
      break
    }

    setNextStart(next)
    setFittingCount(fitting)
  }, [paragraphs, fontSize])   // ← only paragraphs/fontSize; pageStart via ref

  // ── Translation events ────────────────────────────────────────────────────
  useEffect(() => {
    function onTranslated(ev) {
      if (ev.projectPath !== project.path) return
      setParagraphs(ps => ps.map(p =>
        p.index === ev.index ? { ...p, text: ev.text } : p
      ))
      setTranslatingSet(s => { const n = new Set(s); n.delete(ev.index); return n })
      translatedInSession.current += 1
      setTranslatedCount(c => c + 1)
      const bookTotal = project.total ?? 0
      if (bookTotal > 0 && translatedInSession.current + (project.translated ?? 0) >= bookTotal) {
        setBookDone(true)
        if (!localStorage.getItem(exportReminderKey)) {
          localStorage.setItem(exportReminderKey, 'true')
          setShowExportReminder(true)
        }
      }
    }
    return onTranslationComplete(onTranslated)
  }, [project.path, project.total, project.translated, exportReminderKey])

  // ── Trigger translation when switching to target view ────────────────────
  useEffect(() => {
    if (!isSource) {
      TranslateParagraphs(project.path, pageStart.para).catch(() => {})
    }
  }, [isSource, pageStart, project.path])

  // ── Save position on page/view change ────────────────────────────────────
  useEffect(() => {
    SavePosition(project.path, pageStart.para, isSource).catch(() => {})
  }, [project.path, pageStart, isSource])

  // ── Load glossary and bookmarks on open ──────────────────────────────────
  useEffect(() => {
    GetGlossary(project.path).then(g => setGlossary(g || {})).catch(() => {})
  }, [project.path])

  useEffect(() => {
    GetBookmarks(project.path).then(b => setBookmarks(b || [])).catch(() => {})
  }, [project.path])

  // ── Keep-screen-on ────────────────────────────────────────────────────────
  useEffect(() => {
    window.AndroidBridge?.setKeepScreenOn(keepScreenOn)
    localStorage.setItem('babelreader.keepScreenOn', String(keepScreenOn))
  }, [keepScreenOn])

  // ── Overlay ───────────────────────────────────────────────────────────────
  function showOverlayTemporarily() {
    clearTimeout(overlayTimer.current)
    setShowOverlay(true)
    overlayTimer.current = setTimeout(() => setShowOverlay(false), OVERLAY_HIDE)
  }
  function toggleOverlay() {
    if (showOverlay) {
      clearTimeout(overlayTimer.current)
      setShowOverlay(false)
      setShowFontPanel(false)
    } else {
      showOverlayTemporarily()
    }
  }
  useEffect(() => () => clearTimeout(overlayTimer.current), [])

  // ── Navigation ────────────────────────────────────────────────────────────
  // navigating.current is set synchronously so a second tap in the same frame
  // (before React re-renders) is rejected immediately.
  function navigate(delta) {
    if (navigating.current) return

    if (delta > 0) {
      if (!nextStart) return
      navigating.current = true
      setHistory(h => [...h.slice(-99), pageStart])
      setPageStart(nextStart)
    } else {
      if (history.length > 0) {
        navigating.current = true
        const prev = history[history.length - 1]
        setHistory(h => h.slice(0, -1))
        setPageStart(prev)
      } else if (pageStart.para > 0 || pageStart.offset > 0) {
        navigating.current = true
        setPageStart({ para: Math.max(0, pageStart.para - Math.max(1, fittingCount)), offset: 0 })
      }
    }
  }

  // ── Tap handler ───────────────────────────────────────────────────────────
  const isRtl = paragraphs[0]?.direction === 'rtl'

  function handleTap(e) {
    const sel = window.getSelection()
    if (sel && !sel.isCollapsed) {
      sel.removeAllRanges()
      setSelectionPopup(null)
      return
    }
    setSelectionPopup(null)
    const rect = e.currentTarget.getBoundingClientRect()
    const x = e.clientX - rect.left
    const w = rect.width
    if      (x < w * 0.28) isRtl ? navigate(+1) : navigate(-1)
    else if (x > w * 0.72) isRtl ? navigate(-1) : navigate(+1)
    else                   toggleOverlay()
  }

  // ── Font size ─────────────────────────────────────────────────────────────
  function adjustFont(delta) {
    setFontSize(f => {
      const next = Math.min(MAX_FONT, Math.max(MIN_FONT, f + delta))
      localStorage.setItem('babelreader.fontSize', next)
      return next
    })
    showOverlayTemporarily()
  }

  // ── Fix translation ───────────────────────────────────────────────────────
  const fixIndex      = paragraphs[0]?.index ?? 0
  const isTranslating = translatingSet.size > 0

  async function handleFix() {
    toast('Fixing paragraph…', 'info')
    setFixInProgressIndex(fixIndex)
    setTranslatingSet(s => new Set([...s, fixIndex]))
    try {
      await FixTranslation(project.path, fixIndex)
      const batch = await GetParagraphsBatch(project.path, pageStart.para, BATCH, isSource)
      if (batch && batch.length) setParagraphs(batch)
      setTranslatingSet(s => { const n = new Set(s); n.delete(fixIndex); return n })
      toast('Paragraph fixed', 'success')
    } catch (e) {
      toast(`Fix failed: ${e}`, 'error')
      setTranslatingSet(s => { const n = new Set(s); n.delete(fixIndex); return n })
    } finally {
      setFixInProgressIndex(null)
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

  // ── Export as EPUB (from Reader so user doesn't need to go to Library) ─────
  async function handleExportEPUB() {
    toast('Exporting EPUB…', 'info')
    try {
      await ExportProjectEPUB(project.path, null)
      toast('Exported successfully', 'success')
    } catch (e) {
      toast(`Export failed: ${e}`, 'error')
    }
  }

  // ── Text selection → glossary popup ──────────────────────────────────────
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

  // ── Bookmarks ─────────────────────────────────────────────────────────────
  const pageBookmark = bookmarks.find(b => paragraphs.some(p => p.index === b.index))

  async function handleAddBookmark(index, note) {
    try {
      await AddBookmark(project.path, index, note)
      setBookmarks(prev => {
        const filtered = prev.filter(b => b.index !== index)
        return [...filtered, { index, note }].sort((a, b) => a.index - b.index)
      })
      setBookmarkModal(null)
      toast('Bookmark saved', 'success')
    } catch (e) {
      toast(`Bookmark failed: ${e}`, 'error')
    }
  }

  async function handleDeleteBookmark(index) {
    try {
      await DeleteBookmark(project.path, index)
      setBookmarks(prev => prev.filter(b => b.index !== index))
      setBookmarkModal(null)
      setShowBookmarkList(false)
      toast('Bookmark removed', 'success')
    } catch (e) {
      toast(`Remove failed: ${e}`, 'error')
    }
  }

  function openBookmarkModal() {
    const index = paragraphs[0]?.index ?? pageStart.para
    const existing = bookmarks.find(b => b.index === index)
    setBookmarkModal({ index, existing: existing || null })
    setShowOverlay(false)
  }

  function navigateToBookmark(index) {
    setShowBookmarkList(false)
    if (index === pageStart.para && pageStart.offset === 0) return
    setHistory(h => [...h.slice(-99), pageStart])
    setPageStart({ para: index, offset: 0 })
  }

  const paragraphTexts = Object.fromEntries(paragraphs.map(p => [p.index, p.text]))

  // ── Progress ──────────────────────────────────────────────────────────────
  const total    = paragraphs[0]?.total ?? project.total ?? 1
  const lastIdx  = nextStart !== null
    ? (nextStart.offset > 0 ? nextStart.para : nextStart.para - 1)
    : pageStart.para + visibleParagraphs.length - 1
  const percent  = total > 0 ? Math.round(((pageStart.para + 1) / total) * 100) : 0
  const transPct = project.total > 0 ? Math.round((translatedCount / project.total) * 100) : 0
  const isDirect = project.sourceLang === project.targetLang

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div className="reader-page">

      {/* ── Top bar ── */}
      <div className="reader-topbar">
        <button className="reader-back-btn" onClick={onBack}>✕</button>
        <div className="reader-title">{project.title || project.name}</div>
        {!isDirect && (
          <div className="reader-view-toggle">
            <button
              className={`reader-view-btn ${isSource ? 'active' : ''}`}
              onClick={() => setIsSource(true)}
            >{langCode(project.sourceLang)}</button>
            <button
              className={`reader-view-btn ${!isSource ? 'active' : ''}`}
              onClick={() => setIsSource(false)}
            >{langCode(project.targetLang)}</button>
          </div>
        )}
        <button className="theme-btn" onClick={onToggleTheme} title="Toggle theme">
          {theme === 'dark' ? '☀️' : '🌙'}
        </button>
      </div>

      {/* ── Tap area ── */}
      <div className="reader-tap-area" onClick={handleTap}>

        <div className="reader-zone-hint left">{isRtl ? '›' : '‹'}</div>
        <div className="reader-zone-hint right">{isRtl ? '‹' : '›'}</div>

        <div
          className={`reader-text-container${isRtl ? ' rtl' : ''}`}
          onMouseUp={handleTextSelection}
        >
          <div className="reader-text-clip" key={fadeKey} ref={clipRef}>
            {paragraphs[0]?.chapterStart && paragraphs[0]?.index > 0 && pageStart.offset === 0 && (
              <div className="chapter-separator" />
            )}
            {visibleParagraphs.map((p, i) => {
              const displayText = i === 0 && pageStart.offset > 0 && p.text
                ? p.text.slice(pageStart.offset)
                : p.text
              return (
                <p
                  key={p.index}
                  ref={el => { paraRefs.current[i] = el }}
                  className={`reader-text ${isRtl ? 'rtl' : ''} ${!displayText ? 'empty' : ''}`}
                  style={{
                    fontSize: `${fontSize}px`,
                    visibility: i < fittingCount ? 'visible' : 'hidden',
                  }}
                >
                  {translatingSet.has(p.index)
                    ? <span className="spinner" style={{ margin: '6px 0', display: 'inline-block' }} />
                    : (displayText || (isSource ? 'No content' : 'Translating…'))}
                </p>
              )
            })}
          </div>
        </div>

        {/* Progress bars */}
        <div style={{ position: 'absolute', bottom: 0, left: 0, right: 0 }}>
          <div style={{ height: 2, background: 'var(--border)' }}>
            <div style={{ width: `${percent}%`, height: '100%', background: '#4caf82', transition: 'width .4s ease' }} />
          </div>
          {!isDirect && (
            <div style={{ height: 2, background: 'var(--border)' }}>
              <div style={{ width: `${transPct}%`, height: '100%', background: 'var(--accent)', transition: 'width .4s ease' }} />
            </div>
          )}
        </div>

        <div className="reader-progress-label">
          ¶{pageStart.para + 1}–{lastIdx + 1} / {total}
          {isDirect ? ` · ${percent}%` : ` · 📖${percent}% 🔤${transPct}%`}
        </div>

        {/* Overlay */}
        {showOverlay && (
          <div className="reader-overlay" onClick={e => e.stopPropagation()}>
            <div className="overlay-font-ctrl">
              {showFontPanel ? (
                <>
                  <button className="font-btn" onClick={() => adjustFont(-2)}>A−</button>
                  <button className="font-btn" onClick={() => adjustFont(+2)}>A+</button>
                </>
              ) : (
                <button className="font-aa-btn" onClick={() => setShowFontPanel(true)}>Aa</button>
              )}
            </div>
            <div className="overlay-divider" />
            <div className="overlay-actions">
              {!isDirect && !isSource && (
                <button
                  className={`btn overlay-btn${fixInProgressIndex === fixIndex ? ' active' : ''}`}
                  onClick={handleFix}
                  disabled={isTranslating}
                >
                  🔧 Fix
                </button>
              )}
              {!isDirect && bookDone && <button className="btn overlay-btn" onClick={handleSave}>💾 Save</button>}
              {!isDirect && (
                <button className="btn overlay-btn" onClick={handleExportEPUB} title="Export as EPUB">
                  📖 EPUB
                </button>
              )}
              <button
                className={`btn overlay-btn${pageBookmark ? ' active' : ''}`}
                onClick={openBookmarkModal}
                title={pageBookmark ? 'Edit bookmark' : 'Add bookmark'}
              >
                🔖{pageBookmark ? ' ✓' : ''}
              </button>
              {bookmarks.length > 0 && (
                <button className="btn overlay-btn"
                  onClick={() => { setShowOverlay(false); setShowBookmarkList(true) }}
                  title="All bookmarks">
                  📑 {bookmarks.length}
                </button>
              )}
              <button className="btn overlay-btn"
                onClick={() => { setShowOverlay(false); setShowGlossaryList(true) }}
                title="Glossary">
                📖{Object.keys(glossary).length > 0 ? ` ${Object.keys(glossary).length}` : ''}
              </button>
            </div>
          </div>
        )}
      </div>

      {/* ── Modals ── */}

      {selectionPopup && (
        <div className="glossary-popup" style={{ left: selectionPopup.x, top: selectionPopup.y }}>
          <button className="btn" style={{ fontSize: 12, padding: '4px 10px', gap: 4 }}
            onClick={() => openGlossaryModal(selectionPopup.term)}>
            📖 {glossary[selectionPopup.term] ? 'Edit' : 'Add'}
          </button>
        </div>
      )}

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

      {bookmarkModal && (
        <BookmarkAddModal
          index={bookmarkModal.index}
          existing={bookmarkModal.existing}
          onSave={handleAddBookmark}
          onDelete={handleDeleteBookmark}
          onClose={() => setBookmarkModal(null)}
        />
      )}

      {showBookmarkList && (
        <BookmarkListModal
          bookmarks={bookmarks}
          paragraphTexts={paragraphTexts}
          onNavigate={navigateToBookmark}
          onEdit={(b) => { setShowBookmarkList(false); setBookmarkModal({ index: b.index, existing: b }) }}
          onDelete={handleDeleteBookmark}
          onClose={() => setShowBookmarkList(false)}
        />
      )}

      {showGlossaryList && (
        <GlossaryListModal
          glossary={glossary}
          onAdd={() => { setShowGlossaryList(false); setGlossaryModal({ term: '', initial: '', termEditable: true }) }}
          onEdit={(term, trans) => { setShowGlossaryList(false); setGlossaryModal({ term, initial: trans, termEditable: false }) }}
          onClose={() => setShowGlossaryList(false)}
        />
      )}

      {!isDirect && showExportReminder && (
        <div className="modal-backdrop" onClick={e => e.target === e.currentTarget && setShowExportReminder(false)}>
          <div className="modal" style={{ textAlign: 'center', gap: 16 }}>
            <div style={{ fontSize: 40 }}>🎉</div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>Translation complete!</div>
            <div style={{ fontSize: 14, color: 'var(--fg-subtle)' }}>
              The entire book has been translated. Export your project to save a backup.
            </div>
            <div className="modal-actions" style={{ justifyContent: 'center' }}>
              <button className="btn" onClick={() => setShowExportReminder(false)}>Later</button>
              <button className="btn primary" onClick={() => {
                setShowExportReminder(false)
                ExportProject(project.path, null).catch(() => {})
              }}>Export now</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
