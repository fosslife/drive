import { useCallback, useEffect, useState } from 'react'

import { api, href } from './api.js'
import { Viewer } from './browser.jsx'
import { formatDate, formatSize, isImage } from './format.js'
import { Folder, Sheet } from './icons.jsx'

// The screen a visitor with a link sees. No session, no account, nothing that
// can change anything: every request it makes is one of the five public share
// endpoints, and each of those is read-only and confined to the shared subtree.
//
// It is also the only screen a stranger ever judges, so it carries the same
// masthead and the same document as the rest — a public page that looks like a
// different product raises exactly the question a share link should not raise.
export function SharePage({ token }) {
  const base = `/api/shares/${encodeURIComponent(token)}`
  const [info, setInfo] = useState(null)
  const [locked, setLocked] = useState(false)
  const [error, setError] = useState('')

  const open = useCallback(async () => {
    try {
      setInfo(await api(base))
      setLocked(false)
      setError('')
    } catch (err) {
      // 401 on a share is the one case that is not a failure: it means the link
      // wants a password and has told us nothing else about what is behind it.
      if (err.status === 401) setLocked(true)
      else setError(err.message)
    }
  }, [base])

  useEffect(() => {
    open()
  }, [open])

  if (locked) return <Unlock base={base} onUnlocked={open} />
  if (error) return <p className="centred error">{error}</p>
  if (!info) return <p className="centred">Opening the link…</p>

  return (
    <div className="shell public">
      <header>
        <div className="masthead" role="banner">
          <span className="wordmark">drive</span>
          <span className="who">issued to you</span>
          <span className="spacer" />
          {info.expires && <span className="indexing">Available until {formatDate(info.expires)}</span>}
        </div>
      </header>
      {info.kind === 'folder' ? <SharedFolder base={base} name={info.name} /> : <SharedFile base={base} info={info} />}
    </div>
  )
}

function Unlock({ base, onUnlocked }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')

  const submit = async (e) => {
    e.preventDefault()
    try {
      await api(`${base}/unlock`, { method: 'POST', body: { password } })
      onUnlocked()
    } catch (err) {
      setError(err.message)
    }
  }

  return (
    <div className="gate">
      <form className="card" onSubmit={submit}>
        <h1>This link needs a password</h1>
        <p>Whoever sent you the link has the password. Nothing about what is behind it is shown until it matches.</p>
        <label>
          Password
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary">Open</button>
      </form>
    </div>
  )
}

function SharedFile({ base, info }) {
  // An empty path segment is the shared item itself; a file share has nothing
  // below it to name.
  const url = `${base}/download/`
  return (
    <main className="panel single">
      <div className="card">
        <h1>{info.name}</h1>
        <p className="extent-sub">
          {formatSize(info.size)} · {formatDate(info.modified)}
        </p>
        {isImage(info) && <img className="preview" src={url} alt={info.name} />}
        <div className="row-actions">
          <a className="button primary" href={`${url}?download`}>
            Download
          </a>
        </div>
      </div>
    </main>
  )
}

function SharedFolder({ base, name }) {
  // Paths here are relative to the share: the visitor never learns where this
  // folder sits in the owner's drive.
  const [path, setPath] = useState('')
  const [page, setPage] = useState(null)
  const [error, setError] = useState('')
  const [viewing, setViewing] = useState(null)

  useEffect(() => {
    setPage(null)
    api(`${base}/list?path=${encodeURIComponent(path)}`)
      .then(setPage)
      .catch((err) => setError(err.message))
  }, [base, path])

  const entries = page?.entries || []
  const images = entries.filter(isImage)
  const crumbs = path ? path.split('/') : []
  const bytes = entries.reduce((n, e) => n + (e.kind === 'folder' ? 0 : e.size || 0), 0)

  return (
    <main className="panel">
      <div className="toolbar">
        <div className="refhead">
          <div className="crumbs">
            <a href="#" onClick={(e) => (e.preventDefault(), setPath(''))}>
              {name}
            </a>
            {crumbs.map((part, i) => (
              <span key={part + i}>
                <span className="sep">/</span>
                <a href="#" onClick={(e) => (e.preventDefault(), setPath(crumbs.slice(0, i + 1).join('/')))}>
                  {part}
                </a>
              </span>
            ))}
          </div>
        </div>
      </div>
      {error && <p className="error bar">{error}</p>}
      <div className="sheet">
        {!page ? (
          <p className="note">Reading the inventory…</p>
        ) : entries.length === 0 ? (
          <p className="rows-empty">
            <strong>This folder holds nothing.</strong>
            Whoever shared it may add to it later; the link keeps working.
          </p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Title</th>
                <th className="num">Extent</th>
                <th className="num">Modified</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id}>
                  <td title={e.name}>
                    {e.kind === 'folder' ? (
                      <a className="entry" href="#" onClick={(ev) => (ev.preventDefault(), setPath(e.path))}>
                        <span className="mark folder">
                          <Folder />
                        </span>
                        {e.name}
                      </a>
                    ) : isImage(e) ? (
                      <a className="entry" href="#" onClick={(ev) => (ev.preventDefault(), setViewing(e))}>
                        <span className="mark">
                          <img className="icon" loading="lazy" alt="" src={href(`${base}/thumb`, e.path)} />
                        </span>
                        {e.name}
                      </a>
                    ) : (
                      <a className="entry" href={`${href(`${base}/download`, e.path)}?download`}>
                        <span className="mark">
                          <Sheet />
                        </span>
                        {e.name}
                      </a>
                    )}
                  </td>
                  <td className="num">{e.kind === 'folder' ? '' : formatSize(e.size)}</td>
                  <td className="num">{formatDate(e.modified)}</td>
                  <td className="actions">
                    {e.kind === 'file' && <a href={`${href(`${base}/download`, e.path)}?download`}>Download</a>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      <aside className="notes" aria-label="Scope and content">
        <section>
          <h2>Extent</h2>
          <p className="extent-figure live">
            {entries.length.toLocaleString()} {entries.length === 1 ? 'item' : 'items'}
          </p>
          <p className="extent-sub live">{formatSize(bytes)}</p>
        </section>
        <section className="prose">
          <h2>Access and use</h2>
          <p>
            You are reading one folder of someone else's drive, and nothing above or beside it. There is nothing to
            sign in to and nothing here can be changed.
          </p>
          <p>The person who sent this link can revoke it at any time, and it stops working immediately.</p>
        </section>
      </aside>
      {viewing && (
        <Viewer
          entry={viewing}
          images={images}
          onView={setViewing}
          onClose={() => setViewing(null)}
          src={(e) => href(`${base}/download`, e.path)}
        />
      )}
    </main>
  )
}
