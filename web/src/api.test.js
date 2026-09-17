import { test } from 'node:test'
import assert from 'node:assert/strict'

import { explain, href } from './api.js'

// 13.13: each of these must name its cause. The test asserts the words a user
// would need to see, not the exact sentence.
test('a full disk says the disk is full', () => {
  const message = explain(507, 'not enough free space')
  assert.match(message, /disk space/i)
  assert.match(message, /trash|free/i)
})

test('a stale precondition says something changed elsewhere', () => {
  const message = explain(412, 'stale: "notes.txt" is now "3-9"')
  assert.match(message, /changed/i)
  assert.match(message, /reload/i)
})

test('an unexplained failure still names the status rather than nothing', () => {
  assert.match(explain(500, ''), /500/)
  assert.doesNotMatch(explain(500, ''), /undefined|\[object/)
})

test('the server\'s own message is kept when it is the specific one', () => {
  assert.match(explain(409, 'exists: "taxes.pdf"'), /taxes\.pdf/)
})

test('a path with characters a filename may contain survives the round trip', () => {
  assert.equal(href('/api/download', 'a b/c#d?e/f%g'), '/api/download/a%20b/c%23d%3Fe/f%25g')
  assert.equal(href('/api/download', '/leading/and/trailing/'), '/api/download/leading/and/trailing')
})
