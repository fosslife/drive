import { test } from 'node:test'
import assert from 'node:assert/strict'

import { formatSize, isImage, splitExtension } from './format.js'

// Renaming used to put the whole filename in the prompt, so replacing it threw
// the extension away. The stem is what gets offered now, and these are the
// names where "the extension" is not the obvious thing.
test('a name is split into the part worth editing and the part worth keeping', () => {
  assert.deepEqual(splitExtension('taxes.pdf'), ['taxes', '.pdf'])
  assert.deepEqual(splitExtension('backup.tar.gz'), ['backup.tar', '.gz'])
  assert.deepEqual(splitExtension('README'), ['README', ''])
  assert.deepEqual(splitExtension('.gitignore'), ['.gitignore', ''])
  assert.deepEqual(splitExtension('trailing.'), ['trailing', '.'])
})

test('rejoining a split name reproduces it exactly', () => {
  for (const name of ['taxes.pdf', 'backup.tar.gz', 'README', '.gitignore', 'a.b.c.d']) {
    const [stem, ext] = splitExtension(name)
    assert.equal(stem + ext, name)
  }
})

test('sizes read the way a person would say them', () => {
  assert.equal(formatSize(0), '0 B')
  assert.equal(formatSize(1023), '1023 B')
  assert.equal(formatSize(1024), '1.0 KB')
  assert.equal(formatSize(1536), '1.5 KB')
  assert.equal(formatSize(1024 ** 3), '1.0 GB')
})

test('only inert raster images offer a preview', () => {
  assert.ok(isImage({ kind: 'file', name: 'photo.JPG' }))
  assert.ok(!isImage({ kind: 'file', name: 'drawing.svg' }))
  assert.ok(!isImage({ kind: 'file', name: 'paper.pdf' }))
  assert.ok(!isImage({ kind: 'folder', name: 'photo.jpg' }))
})
