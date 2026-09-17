// Fixed-height row windowing. Every row in the file list is the same shape, so
// there is nothing to measure and the visible slice is arithmetic: the rows
// outside it are replaced by two spacers, and the browser lays out a screenful
// instead of a hundred thousand.
//
// ponytail: fixed row height, no measurement, no horizontal virtualisation.
// Swap in a measured implementation only if a row ever needs to vary in height.

export const ROW_HEIGHT = 40

// overscan rows are rendered past each edge so a fast scroll does not show a
// gap before the next frame.
const OVERSCAN = 8

export function windowOf(total, scrollTop, viewportHeight, rowHeight = ROW_HEIGHT) {
  const firstVisible = Math.floor(Math.max(0, scrollTop) / rowHeight)
  const first = Math.max(0, Math.min(total, firstVisible - OVERSCAN))
  const span = Math.ceil(Math.max(0, viewportHeight) / rowHeight) + OVERSCAN * 2
  const last = Math.min(total, first + span)
  return {
    first,
    last,
    padTop: first * rowHeight,
    padBottom: (total - last) * rowHeight,
  }
}
