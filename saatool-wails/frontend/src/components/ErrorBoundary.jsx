import { Component, Fragment } from 'react'

/**
 * ErrorBoundary catches unhandled render errors inside any child component
 * and displays a plain fallback UI instead of crashing the whole app.
 *
 * "Try again" increments retryKey, which is used as the Fragment key below.
 * Changing a key forces React to fully unmount + remount the child subtree,
 * so the retry actually gets a fresh component instance rather than
 * re-rendering the same broken tree.
 */
export default class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { hasError: false, message: '', retryKey: 0 }
  }

  static getDerivedStateFromError(error) {
    return { hasError: true, message: error?.message ?? String(error) }
  }

  componentDidCatch(error, info) {
    console.error('[ErrorBoundary]', error, info?.componentStack)
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center',
          justifyContent: 'center', height: '100%', padding: '2rem', gap: '1rem',
          color: 'var(--text, #eee)', background: 'var(--bg, #1a1a1a)',
        }}>
          <div style={{ fontSize: '2rem' }}>⚠️</div>
          <h2 style={{ margin: 0 }}>Something went wrong</h2>
          <p style={{ margin: 0, opacity: 0.7, fontSize: 13 }}>{this.state.message}</p>
          <button
            style={{ marginTop: '0.5rem', padding: '8px 20px', cursor: 'pointer' }}
            onClick={() => this.setState(s => ({ hasError: false, message: '', retryKey: s.retryKey + 1 }))}
          >
            Try again
          </button>
        </div>
      )
    }
    // The key on Fragment forces a full unmount+remount of children when retryKey changes.
    return <Fragment key={this.state.retryKey}>{this.props.children}</Fragment>
  }
}
