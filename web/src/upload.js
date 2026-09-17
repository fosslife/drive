// A tus 1.0 client for this server's own upload endpoints.
//
// Not tus-js-client: the whole of it is three requests, and the one part that
// actually matters — carrying on from the offset the *server* reports after a
// connection drops, never from the offset this side thinks it reached — is the
// part a library would hide.

import { ApiError, errorFrom } from './api.js'

const TUS_VERSION = '1.0.0'

// 8 MiB per request. The server streams the body straight to disk so the size
// costs it nothing; this side wants chunks small enough that an interruption
// loses little and progress moves visibly, large enough not to pay a round trip
// per megabyte.
const CHUNK = 8 << 20

const sleep = (attempt) => new Promise((r) => setTimeout(r, Math.min(1000 * 2 ** attempt, 15000)))

// b64 encodes a metadata value the way tus wants it: base64 of the UTF-8 bytes.
// btoa alone throws on anything outside Latin-1, which is most filenames people
// actually have.
function b64(value) {
  let binary = ''
  for (const byte of new TextEncoder().encode(value)) binary += String.fromCharCode(byte)
  return btoa(binary)
}

// encodeMetadata builds the Upload-Metadata header. A key whose value is null
// is a flag: the server tests for its presence, not its value.
export function encodeMetadata(meta) {
  return Object.entries(meta)
    .filter(([, value]) => value !== undefined)
    .map(([key, value]) => (value === null ? key : `${key} ${b64(String(value))}`))
    .join(',')
}

async function createUpload(file, { dir, replace, signal, fetch: f }) {
  const res = await f('/api/uploads', {
    method: 'POST',
    headers: {
      'Tus-Resumable': TUS_VERSION,
      'Upload-Length': String(file.size),
      'Upload-Metadata': encodeMetadata({
        dir,
        filename: file.name,
        replace: replace ? null : undefined,
      }),
    },
    signal,
  })
  if (!res.ok) throw await errorFrom(res)
  const location = res.headers.get('Location')
  if (!location) throw new ApiError(res.status, 'the server accepted the upload but did not say where to send it')
  return location
}

// offsetOf asks the server how much of the upload it actually has. This is the
// resume: after an interruption the client's idea of the offset is a guess, and
// a guess that is one byte wrong corrupts the file.
async function offsetOf(location, { signal, fetch: f }) {
  const res = await f(location, {
    method: 'HEAD',
    headers: { 'Tus-Resumable': TUS_VERSION },
    signal,
  })
  if (!res.ok) throw await errorFrom(res)
  return Number(res.headers.get('Upload-Offset'))
}

/**
 * uploadFile stores one file and returns the entry it was stored as — which may
 * carry a different name than was asked for, when something was already there
 * and replacement was not requested.
 *
 * onProgress receives bytes sent so far, not a percentage: the caller has the
 * file size and a byte count is what a resumed upload can report honestly.
 */
export async function uploadFile(file, options = {}) {
  const {
    dir = '',
    replace = false,
    onProgress = () => {},
    signal,
    fetch: f = globalThis.fetch,
    chunkSize = CHUNK,
    retries = 6,
    backoff = sleep,
  } = options
  const transport = { signal, fetch: f }

  const location = await createUpload(file, { dir, replace, ...transport })
  let offset = 0

  for (let attempt = 0; ; ) {
    try {
      while (offset < file.size) {
        const res = await f(location, {
          method: 'PATCH',
          headers: {
            'Tus-Resumable': TUS_VERSION,
            'Content-Type': 'application/offset+octet-stream',
            'Upload-Offset': String(offset),
          },
          // A Blob slice, not the bytes: the browser streams it, so the memory
          // this costs does not scale with the file.
          body: file.slice(offset, Math.min(offset + chunkSize, file.size)),
          signal,
        })
        // 409 means this side and the server disagree about the offset. The
        // server's answer wins, always.
        if (res.status === 409) {
          offset = Number(res.headers.get('Upload-Offset'))
          continue
        }
        if (!res.ok) throw await errorFrom(res)
        offset = Number(res.headers.get('Upload-Offset'))
        onProgress(offset)
        // Progress earns the retry budget back, so a long upload over a flaky
        // link is not killed by its sixth interruption an hour in.
        attempt = 0
        // 201 is the last chunk: the file has been published and the body is
        // the stored entry.
        if (res.status === 201) return res.json()
      }
      throw new ApiError(0, `${file.name}: the server took every byte but never finished the file`)
    } catch (err) {
      // A refusal is a refusal. No space, a name conflict, a revoked session:
      // retrying sends the same bytes into the same answer.
      if (signal?.aborted || err instanceof ApiError) throw err
      if (++attempt > retries) {
        throw new ApiError(0, `${file.name}: the connection kept failing after ${retries} attempts. It will resume from where it stopped if you try again.`)
      }
      await backoff(attempt)
      offset = await offsetOf(location, transport)
      onProgress(offset)
    }
  }
}
