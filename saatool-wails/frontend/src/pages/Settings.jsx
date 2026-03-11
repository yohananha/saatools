import { useState, useEffect } from 'react'
import { GetSettings, SaveSettings } from '../api'
import { toast } from '../App'

function Toggle({ checked, onChange }) {
  return (
    <label className="toggle">
      <input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)} />
      <div className="toggle-track" />
      <div className="toggle-thumb" />
    </label>
  )
}

function Row({ label, hint, children }) {
  return (
    <div className="settings-row">
      <div style={{ flex: 1 }}>
        <div className="settings-row-label">{label}</div>
        {hint && <div className="settings-row-hint">{hint}</div>}
      </div>
      {children}
    </div>
  )
}

function Section({ title, children }) {
  return (
    <div className="settings-section">
      <div className="settings-section-title">{title}</div>
      <div className="settings-card">{children}</div>
    </div>
  )
}

export default function Settings({ theme, onThemeChange }) {
  const [s, setS]       = useState(null)
  const [saving, setSaving] = useState(false)
  const [keepScreenOn, setKeepScreenOnLocal] = useState(() => localStorage.getItem('keepScreenOn') !== 'false')

  useEffect(() => {
    GetSettings().then(setS).catch(e => toast(`Could not load settings: ${e}`, 'error'))
  }, [])

  function set(key, val) {
    setS(prev => ({ ...prev, [key]: val }))
  }

  async function handleSave() {
    if (!s) return
    setSaving(true)
    try {
      await SaveSettings(s)
      // sync theme to live state
      onThemeChange(s.darkMode ? 'dark' : 'light')
      toast('Settings saved', 'success')
    } catch (e) {
      toast(`Save failed: ${e}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  if (!s) return (
    <div style={{ display: 'flex', justifyContent: 'center', padding: 48 }}>
      <div className="spinner" style={{ width: 32, height: 32, borderWidth: 3 }} />
    </div>
  )

  return (
    <div className="settings-page">
      <div className="page-header">
        <span className="page-title">Settings</span>
      </div>

      <Section title="Appearance">
        <Row label="Dark mode" hint="Switch between dark and light themes">
          <Toggle
            checked={s.darkMode}
            onChange={v => set('darkMode', v)}
          />
        </Row>
        <Row label="Font size" hint="Base reading font size (8–36)">
          <input
            type="number" min={8} max={36}
            className="settings-input"
            value={s.appSize}
            onChange={e => set('appSize', parseInt(e.target.value, 10) || 16)}
          />
        </Row>
      </Section>

      <Section title="Languages">
        <Row label="Source language" hint="Default language for imported books">
          <input
            className="settings-input wide"
            placeholder="e.g. English"
            value={s.sourceLanguage}
            onChange={e => set('sourceLanguage', e.target.value)}
          />
        </Row>
        <Row label="Target language" hint="Default translation language">
          <input
            className="settings-input wide"
            placeholder="e.g. Hebrew"
            value={s.targetLanguage}
            onChange={e => set('targetLanguage', e.target.value)}
          />
        </Row>
      </Section>

      <Section title="AI / API">
        <Row label="DeepSeek API key">
          <input
            type="password"
            className="settings-input wide secret"
            placeholder="sk-..."
            value={s.deepSeekAPIKey}
            onChange={e => set('deepSeekAPIKey', e.target.value)}
          />
        </Row>
      </Section>

      <Section title="Reading">
        <Row label="Keep screen on" hint="Prevent screen from sleeping while reading">
          <Toggle
            checked={keepScreenOn}
            onChange={v => {
              setKeepScreenOnLocal(v)
              localStorage.setItem('keepScreenOn', String(v))
              window.AndroidBridge?.setKeepScreenOn(v)
            }}
          />
        </Row>
      </Section>

      <Section title="Translation">
        <Row label="Translate ahead" hint="Paragraphs to pre-translate">
          <input
            type="number" min={0} max={20}
            className="settings-input"
            value={s.translateAhead}
            onChange={e => set('translateAhead', parseInt(e.target.value, 10) || 0)}
          />
        </Row>
        <Row label="Auto proofread" hint="Fix translations automatically after AI generation">
          <Toggle
            checked={s.autoProofread}
            onChange={v => set('autoProofread', v)}
          />
        </Row>
        <Row label="Fix model" hint="Model used when tapping the Fix button">
          <select
            className="settings-input"
            value={s.fixModel || 'deepseek-chat'}
            onChange={e => set('fixModel', e.target.value)}
          >
            <option value="deepseek-chat">Chat (fast)</option>
            <option value="deepseek-reasoner">Reasoner (thorough)</option>
          </select>
        </Row>
        <Row label="Context doc size" hint="Paragraphs of context sent to AI">
          <input
            type="number" min={1} max={20}
            className="settings-input"
            value={s.translationDocSize}
            onChange={e => set('translationDocSize', parseInt(e.target.value, 10) || 3)}
          />
        </Row>
      </Section>

      <div className="settings-save-row">
        <button
          className="btn primary"
          onClick={handleSave}
          disabled={saving}
          style={{ minWidth: 100 }}
        >
          {saving ? <span className="spinner" style={{ width: 16, height: 16, borderWidth: 2 }} /> : null}
          Save
        </button>
      </div>
    </div>
  )
}
