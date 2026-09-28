import { test } from 'node:test'
import assert from 'node:assert/strict'

import { explain, href } from './api.js'

// 13.13: each of these must name its cause. The test asserts the words a user
// would need to see, not the exact sentence.
test('a full disk says the disk is full', () => {
  const message = explain(507, 'insufficient free space: 42 bytes free, this write needs 900')
  assert.match(message, /free space/i)
  assert.match(message, /trash|free/i)
})

// Two limits arrive as 507 and the answer to them is different: delete your own
// files, or ask the operator for room. Neither may be described as the other.
test('a refused upload names the limit that refused it', () => {
  const quota = explain(507, 'account quota exceeded: this upload of 200 bytes does not fit in a quota of 100')
  assert.match(quota, /quota/i)
  assert.doesNotMatch(quota, /disk/i)
  assert.doesNotMatch(explain(507, 'insufficient free space: 42 bytes free'), /quota/i)
})

test('a stale precondition says something changed elsewhere', () => {
  const message = explain(412, 'stale: "notes.txt" is now "3-9"')
  assert.match(message, /changed/i)
  assert.match(message, /reload/i)
})

// 15.5: nginx refuses an 8 MiB upload chunk out of the box. The drive sends no
// 413 of its own, so the message has to point at the proxy and name the setting
// — the alternative was shrinking our chunks to fit someone else's default.
test('a chunk refused by a proxy says which limit to raise', () => {
  const message = explain(413, '')
  assert.match(message, /proxy/i)
  assert.match(message, /client_max_body_size/)
  assert.match(message, /8 MB/)
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
