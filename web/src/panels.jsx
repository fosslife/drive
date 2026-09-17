import { useCallback, useEffect, useState } from 'react'

import { api } from './api.js'
import { formatDate, formatSize } from './format.js'

// useCollection is the shape every one of these screens has: fetch a list, run
// an operation, refetch, and show the reason when the operation is refused.
function useCollection(url, extract = (body) => body) {
  const [items, setItems] = useState([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    try {
      setItems(extract(await api(url)))
      setError('')
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
    // Keyed on the url alone: extract is written inline at each call site and
    // is the same function in behaviour on every render.
  }, [url])

  useEffect(() => {
    reload()
  }, [reload])

  const act = async (fn) => {
    try {
      await fn()
      setError('')
    } catch (err) {
      setError(err.message)
    }
    reload()
  }

  return { items, error, loading, act }
}

function Panel({ title, error, loading, children, actions }) {
  return (
    <main className="panel">
      <div className="toolbar">
        <h1>{title}</h1>
        <span className="spacer" />
        {actions}
      </div>
      {error && <p className="error bar">{error}</p>}
      {loading ? <p className="note">Loading…</p> : children}
    </main>
  )
}

// 13.6: what is in the trash, put back where it came from, or destroyed. This
// is the only screen in the application that can lose a byte, so its wording
// says so and every destructive button confirms.
export function Trash() {
  const { items, error, loading, act } = useCollection('/api/trash', (body) => body.entries || [])

  const empty = () => {
    if (window.confirm(`Permanently delete all ${items.length} item(s)? This cannot be undone.`)) {
      act(() => api('/api/trash', { method: 'DELETE' }))
    }
  }

  return (
    <Panel
      title="Trash"
      error={error}
      loading={loading}
      actions={
        <button disabled={!items.length} onClick={empty}>
          Empty trash
        </button>
      }
    >
      {items.length === 0 ? (
        <p className="note">The trash is empty.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Was at</th>
              <th>Size</th>
              <th>Deleted</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((e) => (
              <tr key={e.id}>
                <td title={e.path}>{e.path}</td>
                <td>{e.kind === 'folder' ? '' : formatSize(e.size)}</td>
                <td>{formatDate(e.deleted)}</td>
                <td className="actions">
                  <button onClick={() => act(() => api(`/api/trash/${e.id}/restore`, { method: 'POST' }))}>
                    Restore
                  </button>
                  <button
                    onClick={() =>
                      window.confirm(`Permanently delete "${e.path}"? This cannot be undone.`) &&
                      act(() => api(`/api/trash/${e.id}`, { method: 'DELETE' }))
                    }
                  >
                    Delete forever
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

// 13.8: the links this account has handed out, and the button that takes one
// back. Revocation is effective on the visitor's next request, so the list is
// the whole administration surface.
export function Shares({ navigate }) {
  const { items, error, loading, act } = useCollection('/api/shares')

  return (
    <Panel title="Share links" error={error} loading={loading}>
      {items.length === 0 ? (
        <p className="note">No share links. Select a file or folder and choose Share.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Target</th>
              <th>Password</th>
              <th>Expires</th>
              <th>Created</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((link) => (
              <tr key={link.id} className={link.state === 'present' ? '' : 'stale'}>
                <td>
                  <a
                    href={`/browse/${encodeURIComponent(link.path)}`}
                    onClick={(e) => (e.preventDefault(), navigate(`/browse/${encodeURIComponent(link.path)}`))}
                  >
                    {link.path}
                  </a>
                  {/* 11.6: a link to something in the trash stops serving, and
                      the owner is told which one rather than left guessing. */}
                  {link.state !== 'present' && <em> — {link.state}, this link no longer works</em>}
                </td>
                <td>{link.protected ? 'Yes' : 'No'}</td>
                <td>{link.expires ? formatDate(link.expires) : 'Never'}</td>
                <td>{formatDate(link.created)}</td>
                <td className="actions">
                  <button onClick={() => act(() => api(`/api/shares/${link.id}`, { method: 'DELETE' }))}>Revoke</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

// 13.8: creating a link. The token comes back exactly once — only its hash is
// stored — so this dialog stays open on the URL until it is copied.
export function ShareDialog({ entry, onClose }) {
  const [password, setPassword] = useState('')
  const [expires, setExpires] = useState('')
  const [link, setLink] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const create = async (e) => {
    e.preventDefault()
    setBusy(true)
    try {
      const body = { path: entry.path, password }
      // An empty expiry is left out rather than sent empty: the field is a
      // timestamp, and "no expiry" is its absence.
      if (expires) body.expires = new Date(expires).toISOString()
      setLink(await api('/api/shares', { method: 'POST', body }))
      setError('')
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  const url = link && new URL(link.url, window.location.origin).href

  return (
    <div className="viewer" onClick={onClose}>
      <form className="card" onClick={(e) => e.stopPropagation()} onSubmit={create}>
        <h1>Share “{entry.name}”</h1>
        {link ? (
          <>
            <p>Anyone with this link can read it. It is shown once and is not stored anywhere it can be read again.</p>
            <input readOnly value={url} onFocus={(e) => e.target.select()} />
            <button type="button" onClick={() => navigator.clipboard?.writeText(url)}>
              Copy link
            </button>
            <button type="button" onClick={onClose}>
              Done
            </button>
          </>
        ) : (
          <>
            <label>
              Password (optional)
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" />
            </label>
            <label>
              Expires (optional)
              <input type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
            </label>
            {error && <p className="error">{error}</p>}
            <button disabled={busy}>{busy ? 'Creating…' : 'Create link'}</button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </>
        )}
      </form>
    </div>
  )
}

// 13.9: API tokens. The secret is in the creation response and nowhere else, so
// it is shown once, plainly labelled as the only time.
export function Tokens() {
  const { items, error, loading, act } = useCollection('/api/tokens')
  const [secret, setSecret] = useState(null)

  const create = () => {
    const name = window.prompt('What is this token for?')
    if (!name) return
    act(async () => setSecret(await api('/api/tokens', { method: 'POST', body: { name } })))
  }

  return (
    <Panel title="API tokens" error={error} loading={loading} actions={<button onClick={create}>New token</button>}>
      {secret && (
        <div className="card">
          <p>
            <strong>Copy “{secret.name}” now.</strong> This is the only time it is shown; the server keeps only a hash
            of it.
          </p>
          <input readOnly value={secret.secret} onFocus={(e) => e.target.select()} />
          <button onClick={() => setSecret(null)}>I have copied it</button>
        </div>
      )}
      {items.length === 0 ? (
        <p className="note">No tokens. A token authenticates as you and reaches exactly your files.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Created</th>
              <th>Last used</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((token) => (
              <tr key={token.id}>
                <td>{token.name}</td>
                <td>{formatDate(token.created_at)}</td>
                <td>{token.last_used_at ? formatDate(token.last_used_at) : 'Never'}</td>
                <td className="actions">
                  <button onClick={() => act(() => api(`/api/tokens/${token.id}`, { method: 'DELETE' }))}>Revoke</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}
