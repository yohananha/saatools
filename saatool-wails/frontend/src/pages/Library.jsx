import { useState, useEffect, useCallback, useRef } from 'react'
import {
  ListProjects, ImportEPUB, ImportProjectFile,
  DeleteProject, ExportProject, OpenEPUBDialog,
  OpenProjectDialog, SaveProjectDialog, LoadProject,
  GetSettings, GetProjectCover,
  GetBookDetailsInfo, FetchBookDetails, SaveBookDetailsInfo,
  isWails,
} from '../api'
import { toast } from '../App'

// ── Helpers ────────────────────────────────────────────────────────────────
const STYLE_STOP = new Set([
  'a','an','the','and','or','but','while','with','of','in','to','is','are',
  'that','this','it','very','which','by','as','at','for','on','its','into',
  'from','also','between','both','some','more','less','often','can','may',
])

function titleCase(s) {
  return s.replace(/\b\w/g, c => c.toUpperCase())
}

/**
 * Extracts the most meaningful 1-3 words from a writing-style phrase,
 * skipping articles/prepositions/conjunctions and applying title case.
 * e.g. "while the pacing varies" → "Pacing Varies"
 *      "clinical and analytical"  → "Clinical Analytical"
 */
function shortenStyle(s) {
  const words = s.trim().split(/\s+/)
  const meaningful = words.filter(w => !STYLE_STOP.has(w.toLowerCase().replace(/[^a-z]/g, '')))
  const selected = (meaningful.length >= 1 ? meaningful : words).slice(0, 3)
  return titleCase(selected.join(' '))
}

/**
 * Normalise a genre tag: trim + title-case, with alias merging for common variants.
 * "sci-fi" / "Sci Fi" / "science fiction" → "Science Fiction"
 */
const GENRE_ALIASES = {
  'sci fi': 'Science Fiction', 'sci-fi': 'Science Fiction',
  'scifi': 'Science Fiction', 'science fiction': 'Science Fiction',
  'sf': 'Science Fiction',
  'spec fic': 'Speculative Fiction', 'speculative fiction': 'Speculative Fiction',
  'lit fic': 'Literary Fiction', 'literary fiction': 'Literary Fiction',
  'ya': 'Young Adult', 'young adult': 'Young Adult',
  'lgbtq': 'LGBTQ+', 'lgbtq+': 'LGBTQ+', 'lgbt': 'LGBTQ+',
  'nonfiction': 'Non-Fiction', 'non fiction': 'Non-Fiction', 'non-fiction': 'Non-Fiction',
  'self help': 'Self-Help', 'self-help': 'Self-Help',
  'historical fiction': 'Historical Fiction', 'hist fiction': 'Historical Fiction',
}
function normalizeGenre(g) {
  const lower = g.trim().toLowerCase()
  return GENRE_ALIASES[lower] || titleCase(g.trim())
}

// ── Accent palette — fallback when no cover image is available ─────────────
const COVERS = [
  ['#7c3aed','#a78bfa'], ['#0369a1','#38bdf8'],
  ['#065f46','#34d399'], ['#9a3412','#fb923c'],
  ['#831843','#f472b6'], ['#1e40af','#60a5fa'],
  ['#3f3f46','#a1a1aa'], ['#713f12','#fbbf24'],
]
function coverColors(name) {
  let h = 0
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) | 0
  return COVERS[Math.abs(h) % COVERS.length]
}

// ── Import Modal ───────────────────────────────────────────────────────────
function ImportModal({ onClose, onImported }) {
  const [mode,       setMode]       = useState('epub')  // 'epub' | 'spz'
  const [path,       setPath]       = useState('')       // display name (or Wails path)
  const [fileObj,    setFileObj]    = useState(null)     // File object in web mode
  const [from,       setFrom]       = useState('')
  const [to,         setTo]         = useState('')
  const [directRead, setDirectRead] = useState(false)   // import without translation
  const [busy,       setBusy]       = useState(false)
  const fileInputRef = useRef(null)

  // Pre-fill with default languages from settings
  useEffect(() => {
    GetSettings().then(s => {
      if (s.sourceLanguage) setFrom(s.sourceLanguage)
      if (s.targetLanguage) setTo(s.targetLanguage)
    }).catch(() => {})
  }, [])

  async function pickFile() {
    if (isWails()) {
      const p = mode === 'epub' ? await OpenEPUBDialog() : await OpenProjectDialog()
      if (p) setPath(p)
    } else {
      fileInputRef.current?.click()
    }
  }

  function handleFileInputChange(e) {
    const file = e.target.files?.[0]
    if (file) { setPath(file.name); setFileObj(file) }
  }

  async function handleImport() {
    if (!path) return
    setBusy(true)
    try {
      let info
      // In Wails mode pass the path string; in web mode pass the File object
      const arg = isWails() ? path : fileObj
      if (mode === 'epub') {
        info = await ImportEPUB(arg, from, directRead ? from : to, directRead)
      } else {
        info = await ImportProjectFile(arg)
      }
      toast(`Imported "${info.title || info.name}"`, 'success')
      onImported(info)
      onClose()
      // Auto-fetch book details for new EPUB imports (fire-and-forget)
      if (mode === 'epub' && info.path) {
        FetchBookDetails(info.path)
          .then(() => toast(`Book details fetched for "${info.title || info.name}"`, 'success'))
          .catch(() => {})
      }
    } catch (e) {
      toast(`Import failed: ${e}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" onClick={e => e.target === e.currentTarget && onClose()}>
      <div className="modal">
        <h2>Import Book</h2>

        <div className="form-row">
          <label>Format</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              className={`btn ${mode === 'epub' ? 'primary' : ''}`}
              onClick={() => setMode('epub')}
            >📖 EPUB</button>
            <button
              className={`btn ${mode === 'spz' ? 'primary' : ''}`}
              onClick={() => setMode('spz')}
            >📦 Project (.spz)</button>
          </div>
        </div>

        <div className="form-row">
          <label>File</label>
          <div className="form-file-row">
            <span className="form-file-path">{path || 'No file selected'}</span>
            <button className="btn" onClick={pickFile}>Browse…</button>
          </div>
          {/* Hidden file input — used in web/Android mode instead of native dialog */}
          <input
            type="file"
            style={{ display: 'none' }}
            ref={fileInputRef}
            accept={mode === 'epub' ? '.epub' : '.spz'}
            onChange={handleFileInputChange}
          />
        </div>

        {mode === 'epub' && (
          <>
            <div className="form-row">
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
                <input
                  type="checkbox"
                  checked={directRead}
                  onChange={e => setDirectRead(e.target.checked)}
                  style={{ width: 16, height: 16, cursor: 'pointer' }}
                />
                Read as-is (no translation)
              </label>
            </div>
            {!directRead && (
              <>
                <div className="form-row">
                  <label>Source language</label>
                  <input
                    className="form-input"
                    placeholder="e.g. English"
                    value={from}
                    onChange={e => setFrom(e.target.value)}
                  />
                </div>
                <div className="form-row">
                  <label>Target language</label>
                  <input
                    className="form-input"
                    placeholder="e.g. Hebrew"
                    value={to}
                    onChange={e => setTo(e.target.value)}
                  />
                </div>
              </>
            )}
          </>
        )}

        <div className="modal-actions">
          <button className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button
            className="btn primary"
            onClick={handleImport}
            disabled={busy || !path}
          >
            {busy ? <span className="spinner" /> : null}
            Import
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Book Info Modal ────────────────────────────────────────────────────────
function BookInfoModal({ info, onClose }) {
  const emptyDetails = { title: info.title || info.name, author: '', genre: '', synopsis: '', writingStyle: '', characters: [] }
  const [details, setDetails] = useState(emptyDetails)
  const [fetching, setSaving] = useState(false)   // fetching from AI
  const [saving,   setSave]   = useState(false)   // saving manually

  // Load current details on open
  useEffect(() => {
    GetBookDetailsInfo(info.path)
      .then(d => setDetails({ ...emptyDetails, ...d }))
      .catch(() => {})
  }, [info.path])

  function setField(key, val) {
    setDetails(d => ({ ...d, [key]: val }))
  }

  // ── Character helpers ──────────────────────────────────────────────────
  function setChar(i, key, val) {
    setDetails(d => {
      const chars = [...(d.characters || [])]
      chars[i] = { ...chars[i], [key]: val }
      return { ...d, characters: chars }
    })
  }
  function addChar() {
    setDetails(d => ({
      ...d,
      characters: [...(d.characters || []), { name: '', gender: '', age: 0, role: '', description: '' }],
    }))
  }
  function removeChar(i) {
    setDetails(d => {
      const chars = [...(d.characters || [])]
      chars.splice(i, 1)
      return { ...d, characters: chars }
    })
  }

  // ── Fetch from AI ──────────────────────────────────────────────────────
  async function handleFetch() {
    setSaving(true)
    try {
      const d = await FetchBookDetails(info.path)
      setDetails({ ...emptyDetails, ...d })
      toast('Book details fetched from AI', 'success')
    } catch (e) {
      toast(`AI fetch failed: ${e}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  // ── Manual save ────────────────────────────────────────────────────────
  async function handleSave() {
    setSave(true)
    try {
      await SaveBookDetailsInfo(info.path, details)
      toast('Book details saved', 'success')
      onClose()
    } catch (e) {
      toast(`Save failed: ${e}`, 'error')
    } finally {
      setSave(false)
    }
  }

  const busy = fetching || saving

  return (
    <div className="modal-backdrop" onClick={e => e.target === e.currentTarget && onClose()}>
      <div className="modal book-info-modal">
        <h2>Book Details — {details.title}</h2>

        {/* Fetch from AI */}
        <button
          className="btn primary"
          style={{ width: '100%', marginBottom: 16, justifyContent: 'center' }}
          onClick={handleFetch}
          disabled={busy}
        >
          {fetching ? <span className="spinner" style={{ width: 14, height: 14, borderWidth: 2 }} /> : '🔄'}
          {fetching ? ' Fetching from AI…' : ' Fetch from AI'}
        </button>

        {/* ── Text fields ── */}
        <div className="form-row">
          <label>Author</label>
          <input className="form-input" value={details.author} placeholder="Author name"
            onChange={e => setField('author', e.target.value)} />
        </div>
        <div className="form-row">
          <label>Genre</label>
          <input className="form-input" value={details.genre} placeholder="e.g. Fantasy, Romance, Thriller"
            onChange={e => setField('genre', e.target.value)} />
        </div>
        <div className="form-row">
          <label>Synopsis</label>
          <textarea className="form-input form-textarea" rows={3} value={details.synopsis}
            placeholder="Brief summary of the book…"
            onChange={e => setField('synopsis', e.target.value)} />
        </div>
        <div className="form-row">
          <label>Writing style</label>
          <textarea className="form-input form-textarea" rows={2} value={details.writingStyle}
            placeholder="e.g. third-person omniscient, dark and introspective, fast-paced"
            onChange={e => setField('writingStyle', e.target.value)} />
        </div>

        {/* ── Characters ── */}
        <div className="form-row" style={{ flexDirection: 'column', alignItems: 'stretch' }}>
          <label style={{ marginBottom: 8 }}>Characters</label>
          {(details.characters || []).map((c, i) => (
            <div key={i} className="char-row">
              <input className="form-input char-name" value={c.name} placeholder="Name"
                onChange={e => setChar(i, 'name', e.target.value)} />
              <input className="form-input char-gender" value={c.gender} placeholder="Gender"
                onChange={e => setChar(i, 'gender', e.target.value)} />
              <input className="form-input char-age" type="number" min={0} value={c.age || ''}
                placeholder="Age" onChange={e => setChar(i, 'age', parseInt(e.target.value, 10) || 0)} />
              <input className="form-input char-role" value={c.role} placeholder="Role"
                onChange={e => setChar(i, 'role', e.target.value)} />
              <button className="book-action-btn" style={{ flexShrink: 0, background: 'rgba(200,50,50,.7)' }}
                onClick={() => removeChar(i)} title="Remove">✕</button>
            </div>
          ))}
          <button className="btn" style={{ marginTop: 6, alignSelf: 'flex-start' }}
            onClick={addChar} disabled={busy}>+ Add character</button>
        </div>

        <div className="modal-actions">
          <button className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button className="btn primary" onClick={handleSave} disabled={busy}>
            {saving ? <span className="spinner" style={{ width: 14, height: 14, borderWidth: 2 }} /> : null}
            💾 Save
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Book Card ──────────────────────────────────────────────────────────────
function BookCard({ info, onOpen, onDelete, onExport, onInfo }) {
  const [dark, light]  = coverColors(info.name)
  const [coverSrc, setCoverSrc] = useState(null)   // null = loading, '' = none, 'data:...' = found

  const readPct  = info.total > 0 ? Math.round((info.readAt  / info.total) * 100) : 0
  const transPct = info.total > 0 ? Math.round((info.translated / info.total) * 100) : 0

  // Lazy-load the cover image
  useEffect(() => {
    let cancelled = false
    GetProjectCover(info.path).then(src => {
      if (!cancelled) setCoverSrc(src || '')
    }).catch(() => {
      if (!cancelled) setCoverSrc('')
    })
    return () => { cancelled = true }
  }, [info.path])

  const hasCover = Boolean(coverSrc)

  return (
    <div className="book-card" onClick={() => onOpen(info)}>
      <div
        className="book-cover"
        style={
          hasCover
            ? { background: '#000', padding: 0 }
            : { background: `linear-gradient(145deg, ${dark}, ${light})` }
        }
      >
        {hasCover ? (
          <img
            src={coverSrc}
            alt={info.title || info.name}
            style={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block' }}
          />
        ) : (
          <>
            <div className="book-spine" />
            {coverSrc === null
              ? <span style={{ fontSize: 10, opacity: 0.5 }}>⋯</span>
              : <span style={{ fontSize: 22 }}>📖</span>
            }
          </>
        )}
      </div>

      <div className="book-meta">
        <div className="book-title">{info.title || info.name}</div>
        {/* Single bar: back layer = longer value, front layer = shorter value */}
        {(() => {
          const backPct  = info.direct ? readPct : Math.max(readPct, transPct)
          const frontPct = info.direct ? 0        : Math.min(readPct, transPct)
          // back is whichever metric is larger; determine its colour
          const backColor  = (!info.direct && transPct >= readPct) ? 'var(--accent)' : '#4caf82'
          const frontColor = backColor === '#4caf82' ? 'var(--accent)' : '#4caf82'
          return (
            <div className="book-progress"
              title={info.direct ? `Read: ${readPct}%` : `Read: ${readPct}% · Translated: ${transPct}%`}>
              <div className="book-progress-back"  style={{ width: `${backPct}%`,  background: backColor }} />
              {frontPct > 0 && (
                <div className="book-progress-front" style={{ width: `${frontPct}%`, background: frontColor }} />
              )}
            </div>
          )
        })()}
      </div>

      <div className="book-actions">
        <button
          className="book-action-btn"
          title="Book details"
          onClick={e => { e.stopPropagation(); onInfo(info) }}
        >ℹ</button>
        <button
          className="book-action-btn"
          title="Export / back up"
          onClick={e => { e.stopPropagation(); onExport(info) }}
        >⬇</button>
        <button
          className="book-action-btn"
          title="Delete"
          style={{ background: 'rgba(200,50,50,.7)' }}
          onClick={e => { e.stopPropagation(); onDelete(info) }}
        >✕</button>
      </div>
    </div>
  )
}

// ── Library Page ───────────────────────────────────────────────────────────
export default function Library({ onOpenReader }) {
  const [projects,     setProjects]     = useState([])
  const [loading,      setLoading]      = useState(true)
  const [showImport,   setShowImport]   = useState(false)
  const [confirmDel,   setConfirmDel]   = useState(null)   // ProjectInfo | null
  const [bookInfo,     setBookInfo]     = useState(null)   // ProjectInfo | null
  const [filterOpen,   setFilterOpen]   = useState(false)
  const [filters,      setFilters]      = useState(() => {
    try {
      const s = JSON.parse(localStorage.getItem('libraryFilters') || '{}')
      return {
        readStatuses:  new Set(s.readStatuses  || []),
        transStatuses: new Set(s.transStatuses || []),
        authors:       new Set(s.authors       || []),
        genres:        new Set(s.genres        || []),
        styles:        new Set(s.styles        || []),
      }
    } catch { return { readStatuses: new Set(), transStatuses: new Set(), authors: new Set(), genres: new Set(), styles: new Set() } }
  })

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const list = await ListProjects()
      setProjects(list || [])
    } catch (e) {
      toast(`Failed to list projects: ${e}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  // Persist filter state
  useEffect(() => {
    localStorage.setItem('libraryFilters', JSON.stringify({
      readStatuses:  [...filters.readStatuses],
      transStatuses: [...filters.transStatuses],
      authors:       [...filters.authors],
      genres:        [...filters.genres],
      styles:        [...filters.styles],
    }))
  }, [filters])

  function toggleFilter(section, value) {
    setFilters(prev => {
      const s = new Set(prev[section])
      if (s.has(value)) s.delete(value)
      else s.add(value)
      return { ...prev, [section]: s }
    })
  }

  function clearFilters() {
    setFilters({ readStatuses: new Set(), transStatuses: new Set(), authors: new Set(), genres: new Set(), styles: new Set() })
  }

  async function handleOpen(info) {
    try {
      const full = await LoadProject(info.path)
      onOpenReader(full)
    } catch (e) {
      toast(`Could not open project: ${e}`, 'error')
    }
  }

  async function handleDelete(info) {
    setConfirmDel(info)
  }

  async function confirmDelete() {
    if (!confirmDel) return
    try {
      await DeleteProject(confirmDel.path)
      toast(`Deleted "${confirmDel.title || confirmDel.name}"`)
      setConfirmDel(null)
      load()
    } catch (e) {
      toast(`Delete failed: ${e}`, 'error')
    }
  }

  async function handleExport(info) {
    try {
      if (isWails()) {
        const dest = await SaveProjectDialog(info.name)
        if (!dest) return
        await ExportProject(info.path, dest)
      } else {
        // Web/Android mode: ExportProject triggers a browser download
        await ExportProject(info.path, null)
      }
      toast('Exported successfully', 'success')
    } catch (e) {
      toast(`Export failed: ${e}`, 'error')
    }
  }

  // ── Filter + sort logic ────────────────────────────────────────────────
  function readStatus(p) {
    const readAt = p.readAt || 0
    if (!p.total || readAt === 0) return 'not_started'
    if (readAt >= p.total) return 'completed'
    return 'in_progress'
  }
  function transStatus(p) {
    if (p.direct) return 'direct'
    if (!p.total || !p.translated) return 'not_started'
    if (p.translated >= p.total) return 'completed'
    return 'in_progress'
  }

  const READ_ORDER   = { in_progress: 0, not_started: 1, completed: 2 }
  const TRANS_ORDER  = { in_progress: 0, not_started: 1, completed: 2, direct: 3 }
  const READ_LABELS  = { in_progress: 'Currently Reading', not_started: 'Not Yet Read', completed: 'Finished Reading' }
  const TRANS_LABELS = { in_progress: 'Translating', not_started: 'Not Translated', completed: 'Fully Translated', direct: 'Direct Read' }

  const isFilterActive = filters.readStatuses.size > 0 || filters.transStatuses.size > 0 ||
                         filters.authors.size > 0 || filters.genres.size > 0 || filters.styles.size > 0

  const allAuthors = [...new Set(projects.map(p => p.author).filter(Boolean))].sort()
  const allGenres  = [...new Set(
    projects.flatMap(p => p.genre ? p.genre.split(',').map(g => normalizeGenre(g)).filter(Boolean) : [])
  )].sort()
  const allStyles  = [...new Set(
    projects.flatMap(p => p.writingStyle ? p.writingStyle.split(',').map(s => shortenStyle(s)).filter(Boolean) : [])
  )].sort()

  const visibleProjects = projects
    .filter(p => {
      if (filters.readStatuses.size  && !filters.readStatuses.has(readStatus(p)))   return false
      if (filters.transStatuses.size && !filters.transStatuses.has(transStatus(p))) return false
      if (filters.authors.size && p.author && !filters.authors.has(p.author)) return false
      if (filters.genres.size) {
        const bookGenres = p.genre ? p.genre.split(',').map(g => normalizeGenre(g)).filter(Boolean) : []
        if (!bookGenres.some(g => filters.genres.has(g))) return false
      }
      if (filters.styles.size) {
        const bookStyles = p.writingStyle ? p.writingStyle.split(',').map(s => shortenStyle(s)).filter(Boolean) : []
        if (!bookStyles.some(s => filters.styles.has(s))) return false
      }
      return true
    })
    .sort((a, b) => {
      const rA = READ_ORDER[readStatus(a)]   ?? 99, rB = READ_ORDER[readStatus(b)]   ?? 99
      if (rA !== rB) return rA - rB
      return (TRANS_ORDER[transStatus(a)] ?? 99) - (TRANS_ORDER[transStatus(b)] ?? 99)
    })

  return (
    <div className="library-page">
      <div className="page-header">
        <span className="page-title">Library</span>
        <button className="btn primary" onClick={() => setShowImport(true)}>
          + Import
        </button>
      </div>

      {projects.length > 0 && (
        <div className="library-filters">
          <button
            className={`filter-toggle-btn${isFilterActive ? ' active' : ''}`}
            onClick={() => setFilterOpen(o => !o)}
          >
            <svg width="13" height="11" viewBox="0 0 13 11" fill="currentColor" aria-hidden="true">
              <path d="M0 0h13v1.8L8 6v5l-3-1.5V6L0 1.8z"/>
            </svg>
            Filters
            {isFilterActive && (
              <span className="filter-badge">
                {filters.readStatuses.size + filters.transStatuses.size + filters.authors.size + filters.genres.size + filters.styles.size}
              </span>
            )}
          </button>
          {isFilterActive && (
            <button className="filter-clear-btn" onClick={clearFilters}>✕ Clear</button>
          )}
        </div>
      )}

      {/* ── Filter side drawer ───────────────────────────────────────────── */}
      {filterOpen && <div className="filter-backdrop" onClick={() => setFilterOpen(false)} />}
      <div className={`filter-drawer${filterOpen ? ' open' : ''}`}>
        <div className="filter-drawer-header">
          <span className="filter-drawer-title">Filters</span>
          <button className="filter-drawer-close" onClick={() => setFilterOpen(false)}>✕</button>
        </div>
        <div className="filter-drawer-body">
          <div className="filter-section">
            <div className="filter-section-title">Reading Progress</div>
            {['in_progress', 'not_started', 'completed'].map(s => (
              <label key={s} className="filter-check-row">
                <input type="checkbox" checked={filters.readStatuses.has(s)}
                  onChange={() => toggleFilter('readStatuses', s)} />
                <span>{READ_LABELS[s]}</span>
              </label>
            ))}
          </div>
          <div className="filter-section">
            <div className="filter-section-title">Translation Progress</div>
            {['in_progress', 'not_started', 'completed', 'direct'].map(s => (
              <label key={s} className="filter-check-row">
                <input type="checkbox" checked={filters.transStatuses.has(s)}
                  onChange={() => toggleFilter('transStatuses', s)} />
                <span>{TRANS_LABELS[s]}</span>
              </label>
            ))}
          </div>
          {allAuthors.length > 0 && (
            <div className="filter-section">
              <div className="filter-section-title">By Author</div>
              {allAuthors.map(a => (
                <label key={a} className="filter-check-row">
                  <input type="checkbox" checked={filters.authors.has(a)}
                    onChange={() => toggleFilter('authors', a)} />
                  <span>{a}</span>
                </label>
              ))}
            </div>
          )}
          {allGenres.length > 0 && (
            <div className="filter-section">
              <div className="filter-section-title">By Genre</div>
              {allGenres.map(g => (
                <label key={g} className="filter-check-row">
                  <input type="checkbox" checked={filters.genres.has(g)}
                    onChange={() => toggleFilter('genres', g)} />
                  <span>{g}</span>
                </label>
              ))}
            </div>
          )}
          {allStyles.length > 0 && (
            <div className="filter-section">
              <div className="filter-section-title">By Writing Style</div>
              {allStyles.map(st => (
                <label key={st} className="filter-check-row">
                  <input type="checkbox" checked={filters.styles.has(st)}
                    onChange={() => toggleFilter('styles', st)} />
                  <span>{st}</span>
                </label>
              ))}
            </div>
          )}
          {isFilterActive && (
            <div style={{ padding: '16px' }}>
              <button className="btn danger" style={{ width: '100%', justifyContent: 'center' }}
                onClick={clearFilters}>✕ Clear all filters</button>
            </div>
          )}
        </div>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 48 }}>
          <div className="spinner" style={{ width: 32, height: 32, borderWidth: 3 }} />
        </div>
      ) : visibleProjects.length === 0 ? (
        projects.length === 0 ? (
          <div className="empty-library">
            <span className="empty-icon">📚</span>
            <p>No books yet</p>
            <button className="btn primary" onClick={() => setShowImport(true)}>
              Import your first book
            </button>
          </div>
        ) : (
          <div className="empty-library">
            <span className="empty-icon">🔍</span>
            <p>No books in this category</p>
          </div>
        )
      ) : (
        <div className="book-grid">
          {visibleProjects.map(p => (
            <BookCard
              key={p.path}
              info={p}
              onOpen={handleOpen}
              onDelete={handleDelete}
              onExport={handleExport}
              onInfo={setBookInfo}
            />
          ))}
        </div>
      )}

      {showImport && (
        <ImportModal
          onClose={() => setShowImport(false)}
          onImported={() => { load(); setShowImport(false) }}
        />
      )}

      {bookInfo && (
        <BookInfoModal
          info={bookInfo}
          onClose={() => setBookInfo(null)}
        />
      )}

      {confirmDel && (
        <div className="modal-backdrop" onClick={e => e.target === e.currentTarget && setConfirmDel(null)}>
          <div className="modal">
            <h2>Delete book?</h2>
            <p style={{ margin: '12px 0 24px', color: 'var(--fg-muted)' }}>
              "{confirmDel.title || confirmDel.name}" will be permanently removed.
            </p>
            <div className="modal-actions">
              <button className="btn" onClick={() => setConfirmDel(null)}>Cancel</button>
              <button className="btn danger" onClick={confirmDelete}>Delete</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
