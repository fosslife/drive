// One fetch wrapper for the whole interface. Every call lands here so that a
// failure is explained in one place rather than thirteen, and so the bundled
// interface stays an ordinary client of the one API.

export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// explain turns a failed response into something that names the cause. The
// three the spec calls out — no disk space, a stale precondition, a refused
// upload — are the ones where "something went wrong" would leave a user with no
// idea what to do differently, so each gets a sentence saying what to do.
export function explain(status, serverMessage) {
  switch (status) {
    case 401:
      return 'Your session has ended. Sign in again.'
    case 403:
      return serverMessage || 'That is not allowed.'
    case 404:
      return serverMessage || 'That is no longer there.'
    case 409:
      return serverMessage || 'Something is already at that name.'
    case 412:
      return 'This changed somewhere else since you loaded it. Reload the folder and try again.'
    case 413:
      // The drive has no request size limit of its own, so this came from
      // something in front of it. nginx's default is 1 MiB and an upload chunk
      // is 8 MiB, which is how this is met in practice — and it is the
      // operator's configuration to fix, not something this client works around
      // by sending smaller chunks.
      return 'A proxy in front of the drive refused the request as too large. Raise its request body limit to at least 8 MB — in nginx, client_max_body_size.'
    case 415:
      return serverMessage || 'There is no preview for this kind of file.'
    case 429:
      return serverMessage || 'Too many attempts. Wait a moment and try again.'
    case 507:
      return `Not enough disk space on the server${serverMessage ? `: ${serverMessage}` : ''}. Free some space or empty the trash, then try again.`
    default:
      return serverMessage || `The server refused the request (${status}).`
  }
}

// errorFrom reads the server's `{"error": "..."}` body when there is one. The
// body is read as text first because an error page from a proxy in front of us
// is not JSON and must not turn into a parse failure with no message.
export async function errorFrom(res) {
  let message = ''
  try {
    const body = await res.text()
    message = JSON.parse(body)?.error || ''
  } catch {
    // Not JSON. The status alone still explains itself.
  }
  return new ApiError(res.status, explain(res.status, message))
}

export async function api(path, { method = 'GET', body, etag, signal } = {}) {
  const headers = {}
  let payload
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  // Only sent when the caller has a version it expects to still be current;
  // without it the API is last-write-wins by design.
  if (etag) headers['If-Match'] = etag

  const res = await fetch(path, { method, headers, body: payload, signal })
  if (!res.ok) throw await errorFrom(res)
  if (res.status === 204) return null
  return res.json()
}

// href builds an API URL with a path segment that may contain anything a
// filename may contain. encodeURIComponent would eat the separators, so each
// segment is encoded on its own.
export function href(prefix, path) {
  const encoded = path.split('/').filter(Boolean).map(encodeURIComponent).join('/')
  return `${prefix}/${encoded}`
}
