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

// ── Internal fetch helper ─────────────────────────────────────────────────
// DELETE requests with a body use POST internally for broader compatibility.

async function apiFetch(method, path, body) {
  const isFormData = body instanceof FormData
  const res = await fetch(BASE + path, {
    method: method === 'DELETE' ? 'POST' : method,
    headers: body && !isFormData
      ? { 'Content-Type': 'application/json', 'X-HTTP-Method': method }
      : {},
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
export const ImportEPUB = (filePath, sourceLang, targetLang) => {
  if (isWails()) return WailsApp.ImportEPUB(filePath, sourceLang, targetLang)
  const fd = new FormData()
  fd.append('file', filePath)
  fd.append('sourceLang', sourceLang)
  fd.append('targetLang', targetLang)
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

/**
 * In Wails mode: saves to destPath chosen by SaveProjectDialog.
 * In web mode:  triggers a browser file download.
 */
export const ExportProject = (path, destPath) => {
  if (isWails()) return WailsApp.ExportProject(path, destPath)
  window.open(BASE + `/api/projects/export?path=${encodeURIComponent(path)}`)
  return Promise.resolve()
}

// ── File dialogs (Wails desktop only) ────────────────────────────────────
// In web mode, components use <input type="file"> directly.

export const OpenEPUBDialog    = () => WailsApp.OpenEPUBDialog()
export const OpenProjectDialog = () => WailsApp.OpenProjectDialog()
export const SaveProjectDialog = (name) => WailsApp.SaveProjectDialog(name)

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
    ws = new WebSocket(`ws://localhost:8766/ws/events`)
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
