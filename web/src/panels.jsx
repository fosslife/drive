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

// The other three series have the same anatomy as the inventory: a heading,
// the operations that apply to the whole series, the sheet itself, and a notes
// column saying what this series is and what its actions really do. The notes
// are the only onboarding anybody gets, so they are written for the relative
// who was handed an account and never read anything else.
function Panel({ title, error, loading, children, actions, reveal, notes }) {
  return (
    <main className="panel">
      <div className="toolbar">
        <div className="refhead">
          <h1>{title}</h1>
          <span className="spacer" />
          {actions}
        </div>
      </div>
      {error && <p className="error bar">{error}</p>}
      {reveal && <div className="reveal">{reveal}</div>}
      <div className="sheet">{loading ? <p className="note">Reading the register…</p> : children}</div>
      <aside className="notes" aria-label="Scope and content">
        {notes}
      </aside>
    </main>
  )
}

const Extent = ({ count, unit, sub }) => (
  <section>
    <h2>Extent</h2>
    <p className="extent-figure live">
      {count.toLocaleString()} {count === 1 ? unit : `${unit}s`}
    </p>
    {sub && <p className="extent-sub live">{sub}</p>}
  </section>
)

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

  const bytes = items.reduce((n, e) => n + (e.kind === 'folder' ? 0 : e.size || 0), 0)

  return (
    <Panel
      title="Trash"
      error={error}
      loading={loading}
      actions={
        <button className="destructive" disabled={!items.length} onClick={empty}>
          Empty trash
        </button>
      }
      notes={
        <>
          <Extent count={items.length} unit="item" sub={`${formatSize(bytes)} still on disk`} />
          <section className="prose">
            <h2>Access and use</h2>
            <p>
              Material withdrawn from the collection. Nothing here has been destroyed — every item still occupies disk
              and goes back to the exact path it came from.
            </p>
            <p className="caveat">
              Delete forever is the only action anywhere in this interface that frees a byte. Items also expire on
              their own once they have sat here for the retention period this drive was started with.
            </p>
          </section>
        </>
      }
    >
      {items.length === 0 ? (
        <p className="rows-empty">
          <strong>Nothing has been withdrawn.</strong>
          Deleting a file in Files moves it here first, and it waits here until you empty it or the retention period
          runs out.
        </p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Was at</th>
              <th className="num">Extent</th>
              <th className="num">Withdrawn</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((e) => (
              <tr key={e.id}>
                <td title={e.path}>{e.path}</td>
                <td className="num">{e.kind === 'folder' ? '' : formatSize(e.size)}</td>
                <td className="num">{formatDate(e.deleted)}</td>
                <td className="actions">
                  <button onClick={() => act(() => api(`/api/trash/${e.id}/restore`, { method: 'POST' }))}>
                    Restore
                  </button>
                  <button
                    className="destructive"
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
  const live = items.filter((l) => l.state === 'present').length

  return (
    <Panel
      title="Share links"
      error={error}
      loading={loading}
      notes={
        <>
          <Extent
            count={items.length}
            unit="link"
            sub={items.length ? `${live.toLocaleString()} still resolving` : null}
          />
          <section className="prose">
            <h2>Access and use</h2>
            <p>
              Issued copies. Anyone holding one of these links can read what it points at without an account, and
              nothing else in your drive.
            </p>
            <p>Revoking takes effect on the visitor's next request. There is nothing to clean up afterwards.</p>
          </section>
        </>
      }
    >
      {items.length === 0 ? (
        <p className="rows-empty">
          <strong>Nothing has been issued.</strong>
          Select one file or folder in Files and choose Share. The link is shown once, and you can take it back here at
          any time.
        </p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Target</th>
              <th>Password</th>
              <th className="num">Expires</th>
              <th className="num">Issued</th>
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
                <td className="num">{link.expires ? formatDate(link.expires) : 'Never'}</td>
                <td className="num">{formatDate(link.created)}</td>
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
        <h1>Issue a link</h1>
        {link ? (
          <>
            <p>
              Anyone with this link can read <strong>{entry.name}</strong>. It is shown once and is not stored anywhere
              it can be read again — copy it now.
            </p>
            <input readOnly value={url} onFocus={(e) => e.target.select()} aria-label="Share link" />
            <div className="row-actions">
              <button className="primary" type="button" onClick={() => navigator.clipboard?.writeText(url)}>
                Copy link
              </button>
              <button type="button" onClick={onClose}>
                Done
              </button>
            </div>
          </>
        ) : (
          <>
            <p>
              A public link to <strong>{entry.name}</strong>. It reaches that item and nothing else, and you can revoke
              it from Share links.
            </p>
            <label>
              Password (optional)
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" />
            </label>
            <label>
              Expires (optional)
              <input type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
            </label>
            {error && <p className="error">{error}</p>}
            <div className="row-actions">
              <button className="primary" disabled={busy}>
                {busy ? 'Creating…' : 'Create link'}
              </button>
              <button type="button" onClick={onClose}>
                Cancel
              </button>
            </div>
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
    <Panel
      title="API tokens"
      error={error}
      loading={loading}
      actions={
        <button className="primary" onClick={create}>
          New token
        </button>
      }
      reveal={
        secret && (
          <div className="card">
            <h1>Copy “{secret.name}” now</h1>
            <p>This is the only time it is shown. The server keeps a hash of it and cannot show it to you again.</p>
            <input readOnly value={secret.secret} onFocus={(e) => e.target.select()} aria-label="Token secret" />
            <div className="row-actions">
              <button className="primary" onClick={() => setSecret(null)}>
                I have copied it
              </button>
            </div>
          </div>
        )
      }
      notes={
        <>
          <Extent count={items.length} unit="token" />
          <section className="prose">
            <h2>Access and use</h2>
            <p>
              Keys of access. A token authenticates as you and reaches exactly your files — the same drive, the same
              limits, no browser session.
            </p>
            <p className="caveat">
              The secret exists in one response and nowhere else. Revoking is immediate; anything still using that
              token stops working at once.
            </p>
          </section>
        </>
      }
    >
      {items.length === 0 ? (
        <p className="rows-empty">
          <strong>No tokens.</strong>
          Create one to reach this drive from a script or another machine without handing over your password.
        </p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th className="num">Issued</th>
              <th className="num">Last used</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((token) => (
              <tr key={token.id}>
                <td>{token.name}</td>
                <td className="num">{formatDate(token.created_at)}</td>
                <td className="num">{token.last_used_at ? formatDate(token.last_used_at) : 'Never'}</td>
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
