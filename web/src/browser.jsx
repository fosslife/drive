import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { api, href } from './api.js'
import { extentParts, formatDate, formatSize, isImage, splitExtension } from './format.js'
import { Cross, Folder, Glass, Left, Plate, Right, Sheet } from './icons.jsx'
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

// What a collection says about itself before anything else: how much of it
// there is. It counts what has actually been listed, and says so when that is
// less than what is there — an extent that quietly reports a page as the whole
// folder is the index claiming to know something the disk has not been asked.
function extentOf(entries) {
  let files = 0
  let folders = 0
  let bytes = 0
  for (const e of entries) {
    if (e.kind === 'folder') folders++
    else {
      files++
      bytes += e.size || 0
    }
  }
  return { files, folders, bytes }
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

  const clearSelection = useCallback(() => {
    anchor.current = null
    setSelected(new Set())
  }, [])

  // Escape drops the selection, the way clicking past the last row does. A
  // selection with no way out but picking a different row is a trap: the
  // toolbar keeps offering to delete something you have stopped pointing at.
  useEffect(() => {
    if (viewing || sharing) return // those screens own Escape while they are up
    const onKey = (e) => e.key === 'Escape' && clearSelection()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [viewing, sharing, clearSelection])

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

  // The extension is held back rather than offered for editing. A prompt cannot
  // preselect just the stem the way an inline rename field does, so the whole
  // name comes up selected and the first keystroke takes ".pdf" with it.
  //
  // ponytail: the cost is that an extension cannot be changed here at all.
  // Both halves go back in the box when rename becomes a real inline field.
  const rename = () => {
    const entry = chosen[0]
    const [stem, ext] = entry.kind === 'folder' ? [entry.name, ''] : splitExtension(entry.name)
    const typed = window.prompt(ext ? `Rename "${entry.name}" to (${ext} is kept)` : `Rename "${entry.name}" to`, stem)
    if (typed === null) return

    const name = typed.trim() + ext
    if (!typed.trim() || name === entry.name) return
    act(() =>
      api('/api/move', {
        method: 'POST',
        body: { from: entry.path, to: join(parentOf(entry.path), name), replace: false },
        etag: entry.etag,
      }),
    )
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
  const extent = useMemo(() => extentOf(entries), [entries])
  const picked = useMemo(() => extentOf(chosen), [chosen])

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

      <div className="head">
        <span className="num" title="Position in this listing">
          №
        </span>
        <span aria-hidden="true" />
        <span>Title</span>
        <span className="extent r">Extent</span>
        <span className="stamp r">Modified</span>
      </div>

      {listing.loading ? (
        <p className="rows-empty">Reading the inventory…</p>
      ) : entries.length === 0 ? (
        <p className="rows-empty">
          <strong>{query ? 'Nothing in the collection matches.' : 'This folder holds nothing yet.'}</strong>
          {query
            ? 'Search reads filenames across everything you own, including folders you have not opened.'
            : 'Drop files anywhere on this page to deposit them here, or use Upload above.'}
        </p>
      ) : (
        <VirtualList count={entries.length} onNearEnd={listing.more} onBlankClick={clearSelection}>
          {(i) => (
            <Row
              key={entries[i].id}
              ordinal={i + 1}
              entry={entries[i]}
              showPath={!!query}
              selected={selected.has(entries[i].id)}
              onSelect={(e) => select(i, e)}
              onOpen={() => open(entries[i])}
            />
          )}
        </VirtualList>
      )}

      <Notes
        query={query}
        extent={extent}
        picked={picked}
        partial={!!listing.next}
        loading={listing.loading}
        indexing={listing.indexing}
        truncated={listing.truncated}
        uploads={uploads}
        onDismiss={(id) => setUploads((prev) => prev.filter((u) => u.id !== id))}
      />

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
      <div className="refhead">
        {/* The path, stated as the reference code of what is on screen. */}
        <div className="crumbs">
          <a href="/" onClick={(e) => (e.preventDefault(), navigate('/'))}>
            Top level
          </a>
          {crumbs.map((name, i) => {
            const to = crumbs.slice(0, i + 1).join('/')
            return (
              <span key={to}>
                <span className="sep">/</span>
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
          <Glass />
          <input
            placeholder="Search every filename you own"
            aria-label="Search every filename you own"
            value={term}
            onChange={(e) => setTerm(e.target.value)}
          />
        </form>
      </div>

      {/* Grouped by what each does to the collection: what adds to it, what
          rearranges or copies out of it, and — set apart — what withdraws. */}
      <div className="ops">
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
        <button className="primary" onClick={() => picker.current.click()}>
          Upload
        </button>
        <button onClick={onNewFolder}>New folder</button>
        <span className="rule" aria-hidden="true" />
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
        <span className="rule" aria-hidden="true" />
        <button className="destructive" disabled={!chosen.length} onClick={onDelete}>
          Delete
        </button>
      </div>
    </div>
  )
}

// Each clause of an extent breakdown is kept whole on its line: the column is
// narrow enough that an unaided wrap strands a numeral from its noun.
const Clauses = ({ of }) => (
  <p className="extent-sub live">
    {extentParts(of).map((clause, i) => (
      <span key={clause}>
        {i > 0 && <span className="sep"> · </span>}
        <span className="clause">{clause}</span>
      </span>
    ))}
  </p>
)

// The notes column. Everything the interface has to say about the listing is
// said here and only here: what is on screen, what is selected, what is
// arriving, and what is not yet true. Nothing in it vanishes on a timer and
// nothing in it pushes the inventory down the page.
function Notes({ query, extent, picked, partial, loading, indexing, truncated, uploads, onDismiss }) {
  const total = extent.files + extent.folders
  return (
    <aside className="notes" aria-label="Scope and content">
      <section>
        <h2>{query ? 'Results' : 'Extent'}</h2>
        {loading ? (
          <p className="extent-sub">Counting…</p>
        ) : (
          <>
            <p className="extent-figure live">
              {total.toLocaleString()} {query ? (total === 1 ? 'result' : 'results') : total === 1 ? 'item' : 'items'}
              {partial ? ' so far' : ''}
            </p>
            <Clauses of={extent} />
          </>
        )}
        {partial && <p>The rest of this folder is listed as you scroll.</p>}
      </section>

      {picked.files + picked.folders > 0 && (
        <section>
          <h2>Selected</h2>
          <p className="extent-figure live">
            {(picked.files + picked.folders).toLocaleString()}{' '}
            {picked.files + picked.folders === 1 ? 'item' : 'items'}
          </p>
          <Clauses of={picked} />
        </section>
      )}

      {/* Accessions: material on its way into the collection, listed while it
          arrives rather than floating over the rows it is about to join. */}
      {uploads.length > 0 && (
        <section>
          <h2>Accessions</h2>
          <div className="uploads">
            {uploads.map((u) => (
              <div key={u.id} className={u.error ? 'upload error' : 'upload'}>
                <span className="name" title={u.name}>
                  {u.name}
                </span>
                {u.error ? (
                  <>
                    <button onClick={() => onDismiss(u.id)}>Dismiss</button>
                    <span>{u.error}</span>
                  </>
                ) : (
                  <>
                    <span className="meta">
                      {formatSize(u.sent)} / {formatSize(u.size)}
                    </span>
                    <progress value={u.sent} max={u.size} />
                  </>
                )}
              </div>
            ))}
          </div>
        </section>
      )}

      {(indexing || truncated) && (
        <section>
          <h2>Incomplete description</h2>
          {indexing && (
            <p className="caveat">
              Still indexing. The reconciler has not finished walking the disk, so this folder may be missing files
              that exist on it.
            </p>
          )}
          {truncated && <p className="caveat">More files match than fit on one page. Narrow the search.</p>}
        </section>
      )}

      <section className="prose">
        <h2>Arrangement</h2>
        <p>
          {query
            ? 'Results are listed with the full path of each item, because where a file sits is the answer to a search.'
            : 'Folders and files as they sit on disk, in the order the filesystem holds them. Nothing here is a copy: the paths are real.'}
        </p>
      </section>
    </aside>
  )
}

function VirtualList({ count, onNearEnd, onBlankClick, children }) {
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
    <div
      className="rows"
      ref={ref}
      onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}
      // A click past the last row is how every file manager drops a selection.
      onClick={(e) => !e.target.closest('.row') && onBlankClick()}
    >
      <div style={{ height: padTop }} />
      {rows}
      <div style={{ height: padBottom }} />
    </div>
  )
}

// One entry of the container list. Every row is this shape and this height —
// the metronome is what lets a hundred thousand of them read as one ruled
// surface instead of a hundred thousand objects.
function Row({ entry, ordinal, selected, showPath, onSelect, onOpen }) {
  const folder = entry.kind === 'folder'
  return (
    <div
      className={`row${selected ? ' on' : ''}${folder ? ' folder' : ''}`}
      style={{ height: ROW_HEIGHT }}
      onClick={onSelect}
      onDoubleClick={onOpen}
      onKeyDown={(e) => e.key === 'Enter' && onOpen()}
      tabIndex={0}
      role="button"
      aria-pressed={selected}
    >
      {/* The position a finding aid prints in its margin. It is also the
          fastest way for two people to talk about the same row. */}
      <span className="num">{ordinal.toLocaleString()}</span>
      <Mark entry={entry} />
      <span className="name" title={entry.path}>
        {showPath ? entry.path : entry.name}
      </span>
      <span className="extent" title={folder ? 'Open the folder for its extent' : undefined}>
        {folder ? '—' : formatSize(entry.size)}
      </span>
      <span className="stamp">{formatDate(entry.modified)}</span>
    </div>
  )
}

// 13.5: `loading="lazy"` is the whole asynchronous story — the browser fetches
// the thumbnail when the row is near the viewport and never blocks a render on
// it. A file with no thumbnail answers 415 and keeps its mark.
function Mark({ entry }) {
  const [failed, setFailed] = useState(false)
  const image = isImage(entry)
  return (
    <span className="mark">
      {entry.kind === 'folder' ? (
        <Folder />
      ) : !image ? (
        <Sheet />
      ) : failed ? (
        <Plate />
      ) : (
        <img
          className="icon"
          loading="lazy"
          decoding="async"
          alt=""
          src={href('/api/thumb', entry.path)}
          onError={() => setFailed(true)}
        />
      )}
    </span>
  )
}

// 13.11: full-size viewing in place, with the folder's other images either side
// of it. The image is served by the same endpoint a download uses, under the
// same access rules; the server decides it may render inline, not this.
//
// Everything said about the item sits on its own plate below the image rather
// than over it: type laid directly on a photograph is legible against some
// photographs and against no others.
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
      <img
        key={entry.id}
        src={src ? src(entry) : href('/api/download', entry.path)}
        alt={entry.name}
        onClick={(e) => e.stopPropagation()}
      />
      <div className="viewer-bar" onClick={(e) => e.stopPropagation()}>
        <span title={entry.name}>
          {entry.name} · {at + 1} of {images.length}
        </span>
        <button disabled={at <= 0} onClick={() => step(-1)} aria-label="Previous image">
          <Left />
        </button>
        <button disabled={at >= images.length - 1} onClick={() => step(1)} aria-label="Next image">
          <Right />
        </button>
        <button onClick={onClose} aria-label="Close">
          <Cross />
        </button>
      </div>
    </div>
  )
}
