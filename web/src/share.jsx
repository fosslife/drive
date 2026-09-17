import { useCallback, useEffect, useState } from 'react'

import { api, href } from './api.js'
import { Viewer } from './browser.jsx'
import { formatDate, formatSize, isImage } from './format.js'

// The screen a visitor with a link sees. No session, no account, nothing that
// can change anything: every request it makes is one of the five public share
// endpoints, and each of those is read-only and confined to the shared subtree.
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
  if (!info) return <p className="centred">Opening…</p>

  return (
    <div className="shell">
      <header>
        <strong>drive</strong>
        <span className="who">shared with you</span>
        <span className="spacer" />
        {info.expires && <span className="note">Available until {formatDate(info.expires)}</span>}
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
    <form className="card centred" onSubmit={submit}>
      <h1>This link needs a password</h1>
      <label>
        Password
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus />
      </label>
      {error && <p className="error">{error}</p>}
      <button>Open</button>
    </form>
  )
}

function SharedFile({ base, info }) {
  // An empty path segment is the shared item itself; a file share has nothing
  // below it to name.
  const url = `${base}/download/`
  return (
    <main className="panel centred">
      <div className="card">
        <h1>{info.name}</h1>
        <p>
          {formatSize(info.size)} · {formatDate(info.modified)}
        </p>
        {isImage(info) && <img className="preview" src={url} alt={info.name} />}
        <a className="button" href={`${url}?download`}>
          Download
        </a>
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

  return (
    <main className="panel">
      <div className="toolbar">
        <div className="crumbs">
          <a href="#" onClick={(e) => (e.preventDefault(), setPath(''))}>
            {name}
          </a>
          {crumbs.map((part, i) => (
            <span key={part + i}>
              {' / '}
              <a href="#" onClick={(e) => (e.preventDefault(), setPath(crumbs.slice(0, i + 1).join('/')))}>
                {part}
              </a>
            </span>
          ))}
        </div>
      </div>
      {error && <p className="error bar">{error}</p>}
      {!page ? (
        <p className="note">Loading…</p>
      ) : entries.length === 0 ? (
        <p className="note">This folder is empty.</p>
      ) : (
        <table>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td>
                  {e.kind === 'folder' ? (
                    <a href="#" onClick={(ev) => (ev.preventDefault(), setPath(e.path))}>
                      📁 {e.name}
                    </a>
                  ) : isImage(e) ? (
                    <a href="#" onClick={(ev) => (ev.preventDefault(), setViewing(e))}>
                      <img className="icon" loading="lazy" alt="" src={href(`${base}/thumb`, e.path)} />
                      {e.name}
                    </a>
                  ) : (
                    <a href={`${href(`${base}/download`, e.path)}?download`}>📄 {e.name}</a>
                  )}
                </td>
                <td>{e.kind === 'folder' ? '' : formatSize(e.size)}</td>
                <td>{formatDate(e.modified)}</td>
                <td className="actions">
                  {e.kind === 'file' && <a href={`${href(`${base}/download`, e.path)}?download`}>Download</a>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
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
