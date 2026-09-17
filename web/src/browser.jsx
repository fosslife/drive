import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { api, href } from './api.js'
import { formatDate, formatSize, isImage } from './format.js'
import { ShareDialog } from './panels.jsx'
import { uploadFile } from './upload.js'
import { ROW_HEIGHT, windowOf } from './virtual.js'

const PAGE = 500

const parentOf = (path) => path.slice(0, Math.max(0, path.lastIndexOf('/')))
const join = (dir, name) => (dir ? `${dir}/${name}` : name)

// useListing pages a folder in as it is scrolled rather than holding the whole
// thing: a folder with 100,000 entries answers its first page immediately, and
// the rest arrives only if someone scrolls that far.
function useListing(path, query) {
  const [state, setState] = useState({ entries: [], next: '', loading: true, indexing: false, truncated: false })
  const [error, setError] = useState('')
  const [epoch, setEpoch] = useState(0)
  // Requests are numbered rather than serialised. Dropping a refresh because an
  // older one is still in flight loses it for good, and the folder then shows
  // something that is no longer true — which is exactly what happens when fifty
  // uploads finish at once.
  const latest = useRef(0)
  const paging = useRef('')

  const load = useCallback(
    async (cursor) => {
      const mine = ++latest.current
      try {
        const url = query
          ? `/api/search?q=${encodeURIComponent(query)}&limit=${PAGE}`
          : `/api/list?path=${encodeURIComponent(path)}&cursor=${encodeURIComponent(cursor || '')}&limit=${PAGE}`
        const page = await api(url)
        if (latest.current !== mine) return // a newer request has taken over
        setState((prev) => ({
          entries: cursor ? [...prev.entries, ...page.entries] : page.entries,
          next: page.next || '',
          loading: false,
          indexing: page.indexing,
          truncated: !!page.truncated,
        }))
        setError('')
      } catch (err) {
        if (latest.current !== mine) return
        setError(err.message)
        setState((prev) => ({ ...prev, loading: false, next: '' }))
      }
    },
    [path, query],
  )

  useEffect(() => {
    setState({ entries: [], next: '', loading: true, indexing: false, truncated: false })
    paging.current = ''
    load('')
  }, [load, epoch])

  // The scroll handler fires this repeatedly at the end of the list; fetching
  // the same cursor twice would append the same page twice.
  const more = useCallback(() => {
    if (!state.next || paging.current === state.next) return
    paging.current = state.next
    load(state.next)
  }, [load, state.next])

  // Stable, so a long-running upload can hold on to it and still refresh the
  // folder it finishes into.
  const reload = useCallback(() => setEpoch((n) => n + 1), [])

  return { ...state, error, setError, more, reload }
}

export function Browser({ route, navigate }) {
  const [pathname, search] = route.split('?')
  const query = new URLSearchParams(search || '').get('q') || ''
  const path = pathname.startsWith('/browse/') ? decodeURIComponent(pathname.slice('/browse/'.length)) : ''

  const listing = useListing(path, query)
  const { entries } = listing
  const [selected, setSelected] = useState(() => new Set())
  const [viewing, setViewing] = useState(null)
  const [sharing, setSharing] = useState(null)
  const [uploads, setUploads] = useState([])
  const anchor = useRef(null)

  useEffect(() => setSelected(new Set()), [path, query])

  const chosen = useMemo(() => entries.filter((e) => selected.has(e.id)), [entries, selected])

  const open = (entry) => {
    if (entry.kind === 'folder') return navigate(`/browse/${encodeURIComponent(entry.path)}`)
    if (isImage(entry)) return setViewing(entry)
    window.location.href = `${href('/api/download', entry.path)}?download`
  }

  // 13.10: click selects, ctrl/cmd-click adds, shift-click takes the range —
  // which is what makes selecting fifty rows one gesture rather than fifty.
  const select = (index, event) => {
    const entry = entries[index]
    setSelected((prev) => {
      if (event.shiftKey && anchor.current !== null) {
        const [from, to] = [anchor.current, index].sort((a, b) => a - b)
        const next = new Set(event.ctrlKey || event.metaKey ? prev : [])
        for (let i = from; i <= to; i++) next.add(entries[i].id)
        return next
      }
      anchor.current = index
      if (event.ctrlKey || event.metaKey) {
        const next = new Set(prev)
        if (!next.delete(entry.id)) next.add(entry.id)
        return next
      }
      return new Set([entry.id])
    })
  }

  // act runs one operation and turns any refusal into a message naming the
  // cause. Every mutation in this screen goes through it, so 13.13 is one place.
  const act = async (fn) => {
    try {
      await fn()
      listing.setError('')
      listing.reload()
      setSelected(new Set())
    } catch (err) {
      listing.setError(err.message)
    }
  }

  // ponytail: prompt() for the three name-a-string operations. It is native,
  // accessible and zero code; replace it with a folder picker when moving into
  // a deep tree by typing its path becomes the thing people complain about.
  const newFolder = () => {
    const name = window.prompt('New folder name')
    if (name) act(() => api('/api/folders', { method: 'POST', body: { path: join(path, name) } }))
  }

  const rename = () => {
    const entry = chosen[0]
    const name = window.prompt(`Rename "${entry.name}" to`, entry.name)
    if (name && name !== entry.name) {
      act(() =>
        api('/api/move', {
          method: 'POST',
          body: { from: entry.path, to: join(parentOf(entry.path), name), replace: false },
          etag: entry.etag,
        }),
      )
    }
  }

  const move = () => {
    const to = window.prompt(`Move ${chosen.length} item(s) into which folder? (blank for the top level)`, path)
    if (to === null) return
    act(async () => {
      for (const entry of chosen) {
        await api('/api/move', { method: 'POST', body: { from: entry.path, to: join(to, entry.name), replace: false } })
      }
    })
  }

  const remove = () => {
    if (!window.confirm(`Move ${chosen.length} item(s) to the trash?`)) return
    act(async () => {
      for (const entry of chosen) {
        await api(href('/api/files', entry.path), { method: 'DELETE', etag: entry.etag })
      }
    })
  }

  const download = () => {
    if (chosen.length === 1 && chosen[0].kind === 'file') {
      window.location.href = `${href('/api/download', chosen[0].path)}?download`
      return
    }
    window.location.href = `/api/archive?${chosen.map((e) => `path=${encodeURIComponent(e.path)}`).join('&')}`
  }

  // 13.4: each file is its own resumable upload, reporting its own progress. A
  // failure of one says which file and why and leaves the rest running.
  const send = useCallback(
    (files) => {
      for (const file of files) {
        const id = `${file.name}:${Date.now()}:${Math.random()}`
        setUploads((prev) => [...prev, { id, name: file.name, size: file.size, sent: 0, error: '' }])
        const patch = (fields) => setUploads((prev) => prev.map((u) => (u.id === id ? { ...u, ...fields } : u)))
        uploadFile(file, { dir: path, onProgress: (sent) => patch({ sent }) })
          .then(() => {
            setUploads((prev) => prev.filter((u) => u.id !== id))
            listing.reload()
          })
          .catch((err) => patch({ error: err.message }))
      }
    },
    [path, listing.reload],
  )

  const images = useMemo(() => entries.filter(isImage), [entries])

  return (
    <main
      className="browser"
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault()
        if (e.dataTransfer.files.length) send([...e.dataTransfer.files])
      }}
    >
      <Toolbar
        path={path}
        query={query}
        navigate={navigate}
        chosen={chosen}
        onNewFolder={newFolder}
        onRename={rename}
        onMove={move}
        onDelete={remove}
        onDownload={download}
        onShare={() => setSharing(chosen[0])}
        onUpload={send}
      />

      {listing.error && (
        <p className="error bar" onClick={() => listing.setError('')}>
          {listing.error}
        </p>
      )}
      {listing.indexing && <p className="note">Still indexing — this folder may be missing files that exist on disk.</p>}
      {listing.truncated && <p className="note">More files match than fit on one page. Narrow the search.</p>}

      <div className="head">
        <span>Name</span>
        <span>Size</span>
        <span>Modified</span>
      </div>

      {listing.loading ? (
        <p className="note">Loading…</p>
      ) : entries.length === 0 ? (
        <p className="note">{query ? 'Nothing matched.' : 'This folder is empty. Drop files here to upload.'}</p>
      ) : (
        <VirtualList count={entries.length} onNearEnd={listing.more}>
          {(i) => (
            <Row
              key={entries[i].id}
              entry={entries[i]}
              showPath={!!query}
              selected={selected.has(entries[i].id)}
              onSelect={(e) => select(i, e)}
              onOpen={() => open(entries[i])}
            />
          )}
        </VirtualList>
      )}

      <Uploads uploads={uploads} onDismiss={(id) => setUploads((prev) => prev.filter((u) => u.id !== id))} />
      {viewing && <Viewer entry={viewing} images={images} onView={setViewing} onClose={() => setViewing(null)} />}
      {sharing && <ShareDialog entry={sharing} onClose={() => setSharing(null)} />}
    </main>
  )
}

function Toolbar({ path, query, navigate, chosen, onNewFolder, onRename, onMove, onDelete, onDownload, onShare, onUpload }) {
  const [term, setTerm] = useState(query)
  useEffect(() => setTerm(query), [query])
  const picker = useRef(null)

  const crumbs = path ? path.split('/') : []
  return (
    <div className="toolbar">
      <div className="crumbs">
        <a href="/" onClick={(e) => (e.preventDefault(), navigate('/'))}>
          Home
        </a>
        {crumbs.map((name, i) => {
          const to = crumbs.slice(0, i + 1).join('/')
          return (
            <span key={to}>
              {' / '}
              <a href={`/browse/${to}`} onClick={(e) => (e.preventDefault(), navigate(`/browse/${encodeURIComponent(to)}`))}>
                {name}
              </a>
            </span>
          )
        })}
      </div>

      {/* 13.7: search is scoped to the whole of the signed-in user's drive, and
          its results carry the full path because that is the answer. */}
      <form
        className="search"
        onSubmit={(e) => {
          e.preventDefault()
          navigate(term ? `/?q=${encodeURIComponent(term)}` : '/')
        }}
      >
        <input placeholder="Search filenames" value={term} onChange={(e) => setTerm(e.target.value)} />
      </form>

      <span className="spacer" />
      <input
        type="file"
        multiple
        hidden
        ref={picker}
        onChange={(e) => {
          onUpload([...e.target.files])
          e.target.value = ''
        }}
      />
      <button onClick={() => picker.current.click()}>Upload</button>
      <button onClick={onNewFolder}>New folder</button>
      <button disabled={chosen.length !== 1} onClick={onRename}>
        Rename
      </button>
      <button disabled={!chosen.length} onClick={onMove}>
        Move
      </button>
      <button disabled={!chosen.length} onClick={onDownload}>
        Download
      </button>
      <button disabled={chosen.length !== 1} onClick={onShare}>
        Share
      </button>
      <button disabled={!chosen.length} onClick={onDelete}>
        Delete
      </button>
    </div>
  )
}

function VirtualList({ count, onNearEnd, children }) {
  const ref = useRef(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [height, setHeight] = useState(600)

  useEffect(() => {
    const el = ref.current
    const measure = () => setHeight(el.clientHeight)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const { first, last, padTop, padBottom } = windowOf(count, scrollTop, height)

  useEffect(() => {
    if (last >= count) onNearEnd()
  }, [last, count, onNearEnd])

  const rows = []
  for (let i = first; i < last; i++) rows.push(children(i))

  return (
    <div className="rows" ref={ref} onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}>
      <div style={{ height: padTop }} />
      {rows}
      <div style={{ height: padBottom }} />
    </div>
  )
}

function Row({ entry, selected, showPath, onSelect, onOpen }) {
  return (
    <div
      className={`row${selected ? ' on' : ''}`}
      style={{ height: ROW_HEIGHT }}
      onClick={onSelect}
      onDoubleClick={onOpen}
      onKeyDown={(e) => e.key === 'Enter' && onOpen()}
      tabIndex={0}
      role="button"
    >
      <Thumb entry={entry} />
      <span className="name" title={entry.path}>
        {showPath ? entry.path : entry.name}
      </span>
      <span>{entry.kind === 'folder' ? '' : formatSize(entry.size)}</span>
      <span>{formatDate(entry.modified)}</span>
    </div>
  )
}

// 13.5: `loading="lazy"` is the whole asynchronous story — the browser fetches
// the thumbnail when the row is near the viewport and never blocks a render on
// it. A file with no thumbnail answers 415 and keeps its icon.
function Thumb({ entry }) {
  const [failed, setFailed] = useState(false)
  if (entry.kind === 'folder') return <span className="icon">📁</span>
  if (failed || !isImage(entry)) return <span className="icon">📄</span>
  return (
    <img
      className="icon"
      loading="lazy"
      decoding="async"
      alt=""
      src={href('/api/thumb', entry.path)}
      onError={() => setFailed(true)}
    />
  )
}

function Uploads({ uploads, onDismiss }) {
  if (!uploads.length) return null
  return (
    <div className="uploads">
      {uploads.map((u) => (
        <div key={u.id} className={u.error ? 'upload error' : 'upload'}>
          <span className="name">{u.name}</span>
          {u.error ? (
            <>
              <span>{u.error}</span>
              <button onClick={() => onDismiss(u.id)}>Dismiss</button>
            </>
          ) : (
            <>
              <progress value={u.sent} max={u.size} />
              <span>
                {formatSize(u.sent)} / {formatSize(u.size)}
              </span>
            </>
          )}
        </div>
      ))}
    </div>
  )
}

// 13.11: full-size viewing in place, with the folder's other images either side
// of it. The image is served by the same endpoint a download uses, under the
// same access rules; the server decides it may render inline, not this.
export function Viewer({ entry, images, onView, onClose, src }) {
  const at = images.findIndex((e) => e.id === entry.id)
  const step = useCallback(
    (by) => {
      const next = images[at + by]
      if (next) onView(next)
    },
    [at, images, onView],
  )

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') onClose()
      if (e.key === 'ArrowLeft') step(-1)
      if (e.key === 'ArrowRight') step(1)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [step, onClose])

  return (
    <div className="viewer" onClick={onClose}>
      <div className="viewer-bar" onClick={(e) => e.stopPropagation()}>
        <button disabled={at <= 0} onClick={() => step(-1)}>
          ←
        </button>
        <span>
          {entry.name} ({at + 1} of {images.length})
        </span>
        <button disabled={at >= images.length - 1} onClick={() => step(1)}>
          →
        </button>
        <button onClick={onClose}>Close</button>
      </div>
      <img src={src ? src(entry) : href('/api/download', entry.path)} alt={entry.name} onClick={(e) => e.stopPropagation()} />
    </div>
  )
}
