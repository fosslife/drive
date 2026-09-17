import { test } from 'node:test'
import assert from 'node:assert/strict'

import { ROW_HEIGHT, windowOf } from './virtual.js'

// 13.3: 100,000 rows scroll smoothly because a screenful is what gets rendered,
// wherever in the list the scroll position is.
test('a huge list only ever renders a screenful', () => {
  const total = 100_000
  for (const scrollTop of [0, 1, 999, 1_000_000, total * ROW_HEIGHT]) {
    const { first, last } = windowOf(total, scrollTop, 800)
    assert.ok(last - first < 50, `rendered ${last - first} rows at ${scrollTop}`)
    assert.ok(first >= 0 && last <= total)
  }
})

test('the spacers always add up to the full list height', () => {
  const total = 100_000
  for (const scrollTop of [0, 4_321, 2_000_000, total * ROW_HEIGHT * 2]) {
    const { first, last, padTop, padBottom } = windowOf(total, scrollTop, 800)
    assert.equal(padTop + (last - first) * ROW_HEIGHT + padBottom, total * ROW_HEIGHT)
  }
})

test('the visible row is inside the window at every scroll position', () => {
  const total = 5_000
  for (let scrollTop = 0; scrollTop < total * ROW_HEIGHT; scrollTop += 137) {
    const { first, last } = windowOf(total, scrollTop, 600)
    const topRow = Math.floor(scrollTop / ROW_HEIGHT)
    const bottomRow = Math.min(total - 1, Math.floor((scrollTop + 600) / ROW_HEIGHT))
    assert.ok(first <= topRow, `row ${topRow} above window ${first}..${last}`)
    assert.ok(last > bottomRow, `row ${bottomRow} below window ${first}..${last}`)
  }
})

test('a short list needs no spacers', () => {
  const { first, last, padTop, padBottom } = windowOf(3, 0, 800)
  assert.deepEqual({ first, last, padTop, padBottom }, { first: 0, last: 3, padTop: 0, padBottom: 0 })
})
