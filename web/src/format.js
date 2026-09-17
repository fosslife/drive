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

export const formatDate = (iso) => new Date(iso).toLocaleString()

// The server's inline allowlist from task 7.8: the raster types a browser
// renders without a scripting context. Anything else is a download, so nothing
// offers to display it.
const VIEWABLE = /\.(jpe?g|png|gif|webp)$/i

export const isImage = (entry) => entry.kind === 'file' && VIEWABLE.test(entry.name)
