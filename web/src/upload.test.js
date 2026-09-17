import { test } from 'node:test'
import assert from 'node:assert/strict'

import { ApiError } from './api.js'
import { encodeMetadata, uploadFile } from './upload.js'

// A tus server in twenty lines: it keeps what arrived, reports the offset it
// actually has, and — when asked — drops the connection partway through a
// chunk, which is the one failure the client has to survive correctly.
function fakeServer({ cutAt = null, refuseWith = null } = {}) {
  const uploads = new Map()
  let cut = false
  let id = 0

  const fetch = async (url, init = {}) => {
    const method = init.method || 'GET'
    if (url === '/api/uploads' && method === 'POST') {
      const key = `/api/uploads/${++id}`
      const size = Number(init.headers['Upload-Length'])
      uploads.set(key, { size, offset: 0, data: new Uint8Array(size) })
      return new Response(null, { status: 201, headers: { Location: key, 'Upload-Offset': '0' } })
    }
    const u = uploads.get(url)
    assert.ok(u, `unknown upload ${url}`)
    if (method === 'HEAD') {
      return new Response(null, { status: 200, headers: { 'Upload-Offset': String(u.offset) } })
    }

    if (refuseWith) {
      return new Response(JSON.stringify({ error: 'not enough free space' }), { status: refuseWith })
    }
    const offset = Number(init.headers['Upload-Offset'])
    if (offset !== u.offset) {
      return new Response(JSON.stringify({ error: 'offset conflict' }), {
        status: 409,
        headers: { 'Upload-Offset': String(u.offset) },
      })
    }

    const bytes = new Uint8Array(await init.body.arrayBuffer())
    if (cutAt !== null && !cut && u.offset + bytes.length > cutAt) {
      // Half the chunk landed before the connection died. The server keeps it,
      // and its offset is now something the client never observed.
      u.data.set(bytes.subarray(0, cutAt - u.offset), u.offset)
      u.offset = cutAt
      cut = true
      throw new TypeError('fetch failed')
    }
    u.data.set(bytes, u.offset)
    u.offset += bytes.length
    const headers = { 'Upload-Offset': String(u.offset) }
    if (u.offset < u.size) return new Response(null, { status: 204, headers })
    return new Response(JSON.stringify({ name: 'stored.bin', size: u.size }), { status: 201, headers })
  }
  return { fetch, uploads }
}

const sourceOf = (size) => {
  const bytes = new Uint8Array(size)
  for (let i = 0; i < size; i++) bytes[i] = (i * 31 + 7) & 0xff
  return bytes
}

const stored = (uploads) => [...uploads.values()][0]

test('a chunked upload stores exactly the source bytes', async () => {
  const bytes = sourceOf(70_000)
  const server = fakeServer()
  const entry = await uploadFile(new File([bytes], 'stored.bin'), {
    fetch: server.fetch,
    chunkSize: 8192,
  })

  assert.equal(entry.name, 'stored.bin')
  assert.deepEqual(stored(server.uploads).data, bytes)
})

// 13.4: the verification for a deliberate network interruption. The client
// resumes from the offset the *server* reports, not from the last offset it saw
// itself — which is short by half a chunk here, and would corrupt the file.
test('an interrupted upload resumes and still stores the source bytes', async () => {
  const bytes = sourceOf(70_000)
  const server = fakeServer({ cutAt: 30_000 })
  const progress = []

  const entry = await uploadFile(new File([bytes], 'stored.bin'), {
    fetch: server.fetch,
    chunkSize: 8192,
    backoff: () => Promise.resolve(),
    onProgress: (sent) => progress.push(sent),
  })

  assert.equal(entry.size, bytes.length)
  assert.deepEqual(stored(server.uploads).data, bytes)
  assert.equal(progress.at(-1), bytes.length)
  // The resume is visible: progress reports the server's offset after the drop.
  assert.ok(progress.includes(30_000), `no resume at the server offset: ${progress}`)
})

test('a full disk stops the upload instead of retrying into the same answer', async () => {
  const server = fakeServer({ refuseWith: 507 })
  let attempts = 0
  const counting = (...args) => {
    attempts++
    return server.fetch(...args)
  }

  await assert.rejects(
    uploadFile(new File([sourceOf(1024)], 'stored.bin'), { fetch: counting, backoff: () => Promise.resolve() }),
    (err) => {
      assert.ok(err instanceof ApiError)
      assert.match(err.message, /disk space/i)
      return true
    },
  )
  // Create plus one refused PATCH. Anything more is a retry loop against a
  // server that already said no.
  assert.equal(attempts, 2)
})

test('metadata carries unicode filenames and the replace flag', () => {
  assert.equal(encodeMetadata({ dir: '', filename: 'naïve.txt' }), 'dir ,filename bmHDr3ZlLnR4dA==')
  assert.equal(encodeMetadata({ filename: 'a', replace: null }), 'filename YQ==,replace')
  assert.equal(encodeMetadata({ filename: 'a', replace: undefined }), 'filename YQ==')
})
