/**
 * api/index.js — Backend abstraction layer
 *
 * Auto-detects the runtime environment:
 *   • Wails desktop  → delegates to wailsjs bindings (window.go exists)
 *   • Android WebView → calls the local Go HTTP server at localhost:8766
 *
 * All components import from here instead of directly from wailsjs,
 * so the same React build works on both desktop and Android.
 */

import * as WailsApp from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'

const BASE = 'http://localhost:8766'

/** Returns true when running inside the Wails desktop shell. */
export const isWails = () =>
  typeof window !== 'undefined' && window.go != null

// Per-process API token injected into the URL by MainActivity (?appToken=…).
// Empty string on Wails desktop (token not used there).
const appToken = (typeof window !== 'undefined')
  ? (new URLSearchParams(window.location.search).get('appToken') || '')
  : ''

// Builds a headers object that always includes X-App-Token when available.
function apiHeaders(extra = {}) {
  return appToken ? { ...extra, 'X-App-Token': appToken } : extra
}

// ── Internal fetch helper ─────────────────────────────────────────────────
// DELETE requests with a body use POST internally for broader compatibility.

async function apiFetch(method, path, body) {
  const isFormData = body instanceof FormData
  const headers = apiHeaders(
    body && !isFormData ? { 'Content-Type': 'application/json', 'X-HTTP-Method': method } : {}
  )
  const res = await fetch(BASE + path, {
    method: method === 'DELETE' ? 'POST' : method,
    headers,
    body: body
      ? (isFormData ? body : JSON.stringify(body))
      : undefined,
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || res.statusText)
  }
  // 204 No Content — return undefined
  const ct = res.headers.get('content-type') || ''
  if (res.status === 204 || !ct.includes('json')) return undefined
  return res.json()
}

// ── Settings ──────────────────────────────────────────────────────────────

export const GetSettings = () =>
  isWails() ? WailsApp.GetSettings()
            : apiFetch('GET', '/api/settings')

export const SaveSettings = (s) =>
  isWails() ? WailsApp.SaveSettings(s)
            : apiFetch('POST', '/api/settings', s)

// ── Projects ──────────────────────────────────────────────────────────────

export const ListProjects = () =>
  isWails() ? WailsApp.ListProjects()
            : apiFetch('GET', '/api/projects')

export const LoadProject = (path) =>
  isWails() ? WailsApp.LoadProject(path)
            : apiFetch('POST', '/api/projects/load', { path })

export const DeleteProject = (path) =>
  isWails() ? WailsApp.DeleteProject(path)
            : apiFetch('POST', '/api/projects/delete', { path })

export const SaveProject = (path) =>
  isWails() ? WailsApp.SaveProject(path)
            : apiFetch('POST', '/api/projects/save', { path })

export const GetProjectCover = (path) =>
  isWails() ? WailsApp.GetProjectCover(path)
            : apiFetch('GET', `/api/projects/cover?path=${encodeURIComponent(path)}`)

// ── Import (file-upload in web mode, file-path in Wails mode) ────────────

/**
 * In Wails mode: filePath is a string (from OpenEPUBDialog).
 * In web mode:  filePath is a File object (from <input type="file">).
 */
export const ImportEPUB = (filePath, sourceLang, targetLang, direct = false) => {
  if (isWails()) return WailsApp.ImportEPUB(filePath, sourceLang, targetLang, direct)
  const fd = new FormData()
  fd.append('file', filePath)
  fd.append('sourceLang', sourceLang)
  fd.append('targetLang', targetLang)
  fd.append('direct', direct ? 'true' : 'false')
  return apiFetch('POST', '/api/projects/import-epub', fd)
}

/**
 * In Wails mode: filePath is a string (from OpenProjectDialog).
 * In web mode:  filePath is a File object (from <input type="file">).
 */
export const ImportProjectFile = (filePath) => {
  if (isWails()) return WailsApp.ImportProjectFile(filePath)
  const fd = new FormData()
  fd.append('file', filePath)
  return apiFetch('POST', '/api/projects/import-spz', fd)
}

// ── Export ────────────────────────────────────────────────────────────────

/** Parse filename from Content-Disposition header; handles quoted values with spaces (e.g. filename="Book - Author - he by Babel.epub"). */
function filenameFromContentDisposition(disp, defaultName) {
  if (!disp) return defaultName
  const m = disp.match(/filename\*=(?:UTF-8'')?([^;\s]+)/i) || disp.match(/filename=(["'])([^"']*)\1/i) || disp.match(/filename=([^;\s]+)/i)
  if (m) {
    if (m[2] !== undefined) return m[2].trim()
    try {
      return (m[1] ? decodeURIComponent(m[1].trim()) : '').trim() || defaultName
    } catch (_) { /* ignore */ }
  }
  return defaultName
}

/**
 * For web/Android: fetch export URL, get full response body, trigger download via <a>, then resolve.
 */
async function exportDownload(exportPath, defaultFilename) {
  const url = BASE + exportPath
  const res = await fetch(url, { headers: apiHeaders() })
  if (!res.ok) {
    const text = await res.text().catch(() => '') || res.statusText
    throw new Error(text || 'Export failed')
  }
  const filename = filenameFromContentDisposition(res.headers.get('Content-Disposition'), defaultFilename) || defaultFilename
  const blob = await res.blob()
  const objectUrl = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objectUrl
  a.download = filename || 'download'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(objectUrl)
}

/** Convert blob to base64 (for Android bridge saveFile). */
function blobToBase64(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const dataUrl = reader.result
      const base64 = dataUrl.indexOf(',') >= 0 ? dataUrl.split(',')[1] : dataUrl
      resolve(base64)
    }
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
}

/** POST export: path in body, avoids query encoding. Use for EPUB on Android so server logs and errors are reliable. */
async function exportEpubPost(path) {
  const res = await fetch(BASE + '/api/projects/export-epub', {
    method: 'POST',
    headers: apiHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ path }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '') || res.statusText
    throw new Error(text || 'Export failed')
  }
  const filename = filenameFromContentDisposition(res.headers.get('Content-Disposition'), 'export.epub')
  const blob = await res.blob()
  const objectUrl = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objectUrl
  a.download = filename
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(objectUrl)
}

/** On Android: choose folder, fetch EPUB, save via bridge so file appears in Downloads (or chosen folder). */
async function exportEpubToAndroid(path) {
  const dir = await chooseFolderOnAndroid()
  if (!dir) return // user cancelled
  const res = await fetch(BASE + '/api/projects/export-epub', {
    method: 'POST',
    headers: apiHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ path }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '') || res.statusText
    throw new Error(text || 'Export failed')
  }
  const filename = filenameFromContentDisposition(res.headers.get('Content-Disposition'), 'export.epub')
  const fallback = (path.split('/').pop() || 'export').replace(/\.[^/.]+$/, '') + '.epub'
  const safeName = filename && /\.epub$/i.test(filename) ? filename : fallback
  const fullPath = dir.endsWith('/') ? dir + safeName : dir + '/' + safeName
  const blob = await res.blob()
  const base64 = await blobToBase64(blob)
  const result = window.AndroidBridge.saveFile(fullPath, base64)
  if (result !== 'ok') throw new Error(result)
}

/** On Android: choose folder, fetch SPZ, save via bridge. */
async function exportProjectToAndroid(path) {
  const dir = await chooseFolderOnAndroid()
  if (!dir) return
  const res = await fetch(BASE + `/api/projects/export?path=${encodeURIComponent(path)}`, { headers: apiHeaders() })
  if (!res.ok) {
    const text = await res.text().catch(() => '') || res.statusText
    throw new Error(text || 'Export failed')
  }
  const filename = filenameFromContentDisposition(res.headers.get('Content-Disposition'), 'project.spz')
  const fallback = (path.split('/').pop() || 'project').replace(/\.[^/.]+$/, '') + '.spz'
  const safeName = filename && /\.spz$/i.test(filename) ? filename : fallback
  const fullPath = dir.endsWith('/') ? dir + safeName : dir + '/' + safeName
  const blob = await res.blob()
  const base64 = await blobToBase64(blob)
  const result = window.AndroidBridge.saveFile(fullPath, base64)
  if (result !== 'ok') throw new Error(result)
}

/**
 * In Wails mode: saves to destPath chosen by SaveProjectDialog.
 * Android: folder picker then save via bridge. Web: fetch + programmatic download.
 */
export const ExportProject = (path, destPath) => {
  if (isWails()) return WailsApp.ExportProject(path, destPath)
  if (typeof window !== 'undefined' && window.AndroidBridge) return exportProjectToAndroid(path)
  return exportDownload(`/api/projects/export?path=${encodeURIComponent(path)}`, 'project.spz')
}

/**
 * Export as EPUB (translation + metadata). Wails: save dialog then write. Android: folder picker then save via bridge. Web: POST + programmatic download.
 */
export const ExportProjectEPUB = (path, destPath) => {
  if (isWails()) return WailsApp.ExportProjectEPUB(path, destPath)
  if (typeof window !== 'undefined' && window.AndroidBridge) return exportEpubToAndroid(path)
  return exportEpubPost(path)
}

/** On Android: choose folder, fetch TXT, save via bridge. */
async function exportTxtToAndroid(path) {
  const dir = await chooseFolderOnAndroid()
  if (!dir) return
  const res = await fetch(BASE + `/api/projects/export-txt?path=${encodeURIComponent(path)}`, { headers: apiHeaders() })
  if (!res.ok) {
    const text = await res.text().catch(() => '') || res.statusText
    throw new Error(text || 'Export failed')
  }
  const filename = filenameFromContentDisposition(res.headers.get('Content-Disposition'), 'translation.txt')
  const fallback = (path.split('/').pop() || 'translation').replace(/\.[^/.]+$/, '') + '-translation.txt'
  const safeName = filename && /\.(txt|text)$/i.test(filename) ? filename : fallback
  const fullPath = dir.endsWith('/') ? dir + safeName : dir + '/' + safeName
  const blob = await res.blob()
  const base64 = await blobToBase64(blob)
  const result = window.AndroidBridge.saveFile(fullPath, base64)
  if (result !== 'ok') throw new Error(result)
}

/**
 * Export translation only as TXT. Wails: save dialog then write. Android: folder picker then save via bridge. Web: fetch + download.
 */
export const ExportProjectTranslationOnly = (path, destPath) => {
  if (isWails()) return WailsApp.ExportProjectTranslationOnly(path, destPath)
  if (typeof window !== 'undefined' && window.AndroidBridge) return exportTxtToAndroid(path)
  return exportDownload(`/api/projects/export-txt?path=${encodeURIComponent(path)}`, 'translation.txt')
}

// ── File dialogs (Wails desktop only) ────────────────────────────────────
// In web mode, components use <input type="file"> directly.

export const OpenEPUBDialog    = () => WailsApp.OpenEPUBDialog()
export const OpenFolderDialog  = () => WailsApp.OpenFolderDialog()
export const OpenProjectDialog = () => WailsApp.OpenProjectDialog()

/** On Android: show folder presets dialog; returns a Promise that resolves with the chosen path or null. */
export function chooseFolderOnAndroid() {
  if (typeof window === 'undefined' || !window.AndroidBridge) return Promise.resolve(null)
  return new Promise((resolve) => {
    window.onFolderChosen = (path) => {
      window.onFolderChosen = null
      resolve(path || null)
    }
    window.AndroidBridge.showFolderPresets()
  })
}
export const SaveProjectDialog = (name) => WailsApp.SaveProjectDialog(name)
export const SaveEPUBDialog    = (name) => WailsApp.SaveEPUBDialog(name)
export const SaveTextDialog    = (name) => WailsApp.SaveTextDialog(name)

// ── Paragraphs ────────────────────────────────────────────────────────────

export const GetParagraphsBatch = (path, from, count, source) =>
  isWails() ? WailsApp.GetParagraphsBatch(path, from, count, source)
            : apiFetch('GET',
                `/api/paragraphs/batch?path=${encodeURIComponent(path)}&from=${from}&count=${count}&source=${source}`)

export const GetLastPosition = (path) =>
  isWails() ? WailsApp.GetLastPosition(path)
            : apiFetch('GET', `/api/paragraphs/position?path=${encodeURIComponent(path)}`)

export const SavePosition = (path, index, sourceView) =>
  isWails() ? WailsApp.SavePosition(path, index, sourceView)
            : apiFetch('POST', '/api/paragraphs/position', { path, index, sourceView })

export const TranslateParagraphs = (path, from) =>
  isWails() ? WailsApp.TranslateParagraphs(path, from)
            : apiFetch('POST', '/api/paragraphs/translate', { path, from })

/** Set the project allowed to run translation (e.g. book open in Reader). Pass '' when leaving Reader. */
export const SetActiveProject = (path) =>
  isWails() ? WailsApp.SetActiveProject(path || '')
            : apiFetch('POST', '/api/translation/active', { path: path || '' })

/** Translate the entire book in the background. Stops when user opens another book. */
export const TranslateWholeBook = (path) =>
  isWails() ? WailsApp.TranslateWholeBook(path)
            : apiFetch('POST', '/api/translation/whole-book', { path })

export const FixTranslation = (path, index) =>
  isWails() ? WailsApp.FixTranslation(path, index)
            : apiFetch('POST', '/api/paragraphs/fix', { path, index })

// ── Book details ──────────────────────────────────────────────────────────

export const GetBookDetailsInfo = (path) =>
  isWails() ? WailsApp.GetBookDetailsInfo(path)
            : apiFetch('GET', `/api/book?path=${encodeURIComponent(path)}`)

export const SaveBookDetailsInfo = (path, info) =>
  isWails() ? WailsApp.SaveBookDetailsInfo(path, info)
            : apiFetch('POST', '/api/book', { path, ...info })

export const FetchBookDetails = (path) =>
  isWails() ? WailsApp.FetchBookDetails(path)
            : apiFetch('POST', '/api/book/fetch', { path })

// ── Glossary ──────────────────────────────────────────────────────────────

export const GetGlossary = (path) =>
  isWails() ? WailsApp.GetGlossary(path)
            : apiFetch('GET', `/api/glossary?path=${encodeURIComponent(path)}`)

export const SetGlossaryEntry = (path, term, targetTerm) =>
  isWails() ? WailsApp.SetGlossaryEntry(path, term, targetTerm)
            : apiFetch('POST', '/api/glossary', { path, term, targetTerm })

export const DeleteGlossaryEntry = (path, term) =>
  isWails() ? WailsApp.DeleteGlossaryEntry(path, term)
            : apiFetch('DELETE', '/api/glossary', { path, term })

// ── Bookmarks ─────────────────────────────────────────────────────────────

export const GetBookmarks = (path) =>
  isWails() ? WailsApp.GetBookmarks(path)
            : apiFetch('GET', `/api/bookmarks?path=${encodeURIComponent(path)}`)

export const AddBookmark = (path, index, note) =>
  isWails() ? WailsApp.AddBookmark(path, index, note)
            : apiFetch('POST', '/api/bookmarks', { path, index, note })

export const DeleteBookmark = (path, index) =>
  isWails() ? WailsApp.DeleteBookmark(path, index)
            : apiFetch('DELETE', '/api/bookmarks', { path, index })

// ── Log ───────────────────────────────────────────────────────────────────

export const GetLog = () =>
  isWails() ? WailsApp.GetLog()
            : apiFetch('GET', '/api/log')

export const TestNotification = () =>
  isWails() ? WailsApp.TestNotification()
            : apiFetch('POST', '/api/log/test-notification')

// ── Translation events ────────────────────────────────────────────────────

/**
 * Subscribe to translation:complete events.
 * Returns a cleanup/unsubscribe function — use as useEffect return value.
 *
 * In Wails mode: uses Wails EventsOn/EventsOff.
 * In web mode:  opens a WebSocket to /ws/events.
 */
export function onTranslationComplete(handler) {
  if (isWails()) {
    EventsOn('translation:complete', handler)
    return () => EventsOff('translation:complete', handler)
  }

  // Web/Android mode — connect to Go WebSocket
  let ws
  let stopped = false

  function connect() {
    if (stopped) return
    ws = new WebSocket(`ws://localhost:8766/ws/events${appToken ? '?token=' + appToken : ''}`)
    ws.onmessage = (e) => {
      try {
        const ev = JSON.parse(e.data)
        if (ev.type === 'translation:complete') handler(ev.data)
      } catch { /* ignore malformed frames */ }
    }
    ws.onclose = () => {
      // Auto-reconnect after 2 s if not intentionally closed
      if (!stopped) setTimeout(connect, 2000)
    }
  }
  connect()

  return () => {
    stopped = true
    ws?.close()
  }
}
