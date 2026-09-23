// The three things every screen needs to say about an entry. They live apart
// from the screens so that the file browser and the panels can both use them
// without importing each other.

export const formatSize = (bytes) => {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let n = bytes / 1024
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n < 10 ? n.toFixed(1) : Math.round(n)} ${units[i]}`
}

// A listing is scanned, not read, and "23/09/2026, 11:49:02" on every row is
// twelve characters of noise around the two that differ. Inside the current
// year the time is what distinguishes two files; outside it, the year is.
export const formatDate = (iso) => {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  const thisYear = at.getFullYear() === new Date().getFullYear()
  const day = at.toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
    ...(thisYear ? {} : { year: 'numeric' }),
  })
  if (!thisYear) return day
  return `${day}, ${at.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`
}

// The sub-line of an extent statement, as its separate clauses: how the count
// breaks down, with the parts that are zero left out rather than printed as
// zero. It is returned unjoined so each clause can be kept whole on one line —
// a figure split from its noun ("3 / folders") is the one thing an extent
// statement must never do.
export const extentParts = ({ files, folders, bytes }) => {
  const parts = []
  if (files) parts.push(`${files.toLocaleString()} ${files === 1 ? 'file' : 'files'}`)
  if (folders) parts.push(`${folders.toLocaleString()} ${folders === 1 ? 'folder' : 'folders'}`)
  if (bytes || !parts.length) parts.push(formatSize(bytes))
  return parts
}

// The server's inline allowlist from task 7.8: the raster types a browser
// renders without a scripting context. Anything else is a download, so nothing
// offers to display it.
const VIEWABLE = /\.(jpe?g|png|gif|webp)$/i

export const isImage = (entry) => entry.kind === 'file' && VIEWABLE.test(entry.name)

// splitExtension returns [stem, extension]. A leading dot is part of the name,
// not an extension — ".gitignore" is a file called .gitignore — and only the
// last suffix counts, so "backup.tar.gz" keeps ".gz".
export function splitExtension(name) {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, '']
}
