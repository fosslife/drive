// 15.8: a file someone uploaded must not be able to run script in the
// application's own origin — not opened directly, not opened through a share
// link, not framed by a page of ours. That is the stored-XSS hole every
// self-hosted drive has had a CVE for, and the only honest way to check it is
// in a browser: a header assertion says what we sent, not what Chrome did with
// it.

import assert from 'node:assert/strict'
import { after, before, describe, test } from 'node:test'

import { browser, chromePath, signUp, start } from './harness.js'

const USER = 'ada'
const PASSWORD = 'correct horse battery staple'

// Each payload calls out to /pwned if it ever runs. Nothing on this server
// answers that path, which does not matter: the request being attempted at all
// is the failure.
const PAYLOADS = {
  'evil.html': '<!doctype html><script>fetch("/pwned?from=html&c=" + document.cookie)</script>',
  'evil.svg':
    '<svg xmlns="http://www.w3.org/2000/svg"><script>fetch("/pwned?from=svg&c=" + document.cookie)</script></svg>',
  // A polyglot: a real GIF header, so the extension, the magic bytes and the
  // allowlist all agree, with script spliced in behind it. This is the file
  // that gets served inline, and the one a sniffing browser would run.
  'polyglot.gif': 'GIF89a=1;/*<script>fetch("/pwned?from=gif&c=" + document.cookie)</script>*/',
}

describe('stored content in the browser', { skip: chromePath() ? false : 'no Chrome on this machine' }, () => {
  let drive
  let chrome
  let page
  let attempts = []
  const shares = {}

  before(async () => {
    drive = await start()
    chrome = await browser()
    page = await chrome.newPage()
    page.setDefaultTimeout(20000)
    page.on('request', (r) => {
      if (r.url().includes('/pwned')) attempts.push(r.url())
    })
    // An attachment is a download, and a download this test does not want on
    // disk. Denying it leaves the navigation aborted, which is the point.
    const cdp = await page.createCDPSession()
    await cdp.send('Browser.setDownloadBehavior', { behavior: 'deny' })

    await signUp(page, drive, USER, PASSWORD)

    // Uploaded through the page, so the session cookie and the same-origin
    // headers are the browser's own: this is a user uploading a file.
    for (const [name, content] of Object.entries(PAYLOADS)) {
      const status = await page.evaluate(
        async (name, content) => {
          const created = await fetch('/api/uploads', {
            method: 'POST',
            headers: {
              'Tus-Resumable': '1.0.0',
              'Upload-Length': String(new Blob([content]).size),
              'Upload-Metadata': `filename ${btoa(name)},dir ${btoa('')}`,
            },
          })
          const sent = await fetch(created.headers.get('Location'), {
            method: 'PATCH',
            headers: {
              'Tus-Resumable': '1.0.0',
              'Content-Type': 'application/offset+octet-stream',
              'Upload-Offset': '0',
            },
            body: content,
          })
          return sent.status
        },
        name,
        content,
      )
      assert.equal(status, 201, `uploading ${name}`)

      shares[name] = await page.evaluate(async (path) => {
        const made = await fetch('/api/shares', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path }),
        })
        return (await made.json()).token
      }, name)
      assert.ok(shares[name], `no share link for ${name}`)
    }
  })

  after(async () => {
    await chrome?.close()
    drive?.stop()
  })

  // headers of one URL, read from inside the page so the request is the same
  // one the browser would make.
  const headersOf = (url) =>
    page.evaluate(async (u) => {
      const r = await fetch(u)
      return Object.fromEntries(r.headers)
    }, url)

  const urls = (name) => [`/api/download/${name}`, `/api/shares/${shares[name]}/download/`]

  test('every stored file is served with the headers that stop it executing', async () => {
    for (const name of Object.keys(PAYLOADS)) {
      for (const url of urls(name)) {
        const h = await headersOf(url)
        assert.equal(h['x-content-type-options'], 'nosniff', `${url} may be sniffed`)
        assert.match(h['content-security-policy'], /default-src 'none'/, `${url} has no policy`)

        if (name.endsWith('.gif')) {
          // On the inline allowlist, because a raster image is inert — but it
          // is served as an image and nothing else, whatever is inside it.
          assert.equal(h['content-type'], 'image/gif', `${url} is not typed as an image`)
        } else {
          assert.match(h['content-disposition'], /^attachment/, `${url} renders in the origin`)
          assert.equal(h['content-type'], 'application/octet-stream', `${url} is typed as something a browser runs`)
        }
      }
    }
  })

  test('no uploaded file runs script, opened directly or through its share link', async () => {
    attempts = []
    for (const name of Object.keys(PAYLOADS)) {
      for (const path of urls(name)) {
        // An attachment aborts the navigation rather than rendering, which is
        // itself the defence: catch it and carry on.
        await page.goto(`${drive.base}${path}`, { waitUntil: 'load' }).catch(() => {})
        if (name.endsWith('.gif')) {
          // The one that does render: as an image, never as a document.
          assert.equal(await page.evaluate(() => document.contentType), 'image/gif')
        }
        await new Promise((r) => setTimeout(r, 300))
        assert.deepEqual(attempts, [], `${path} executed its payload`)
      }
    }
  })

  test('no uploaded file runs script when framed by a page of ours', async () => {
    attempts = []
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    for (const name of Object.keys(PAYLOADS)) {
      for (const path of urls(name)) {
        await page.evaluate(
          (src) =>
            new Promise((resolve) => {
              const frame = document.createElement('iframe')
              frame.src = src
              frame.onload = frame.onerror = () => resolve()
              document.body.append(frame)
              setTimeout(resolve, 500)
            }),
          path,
        )
      }
    }
    await new Promise((r) => setTimeout(r, 300))
    assert.deepEqual(attempts, [], 'a framed upload executed its payload')
  })

  test('the session cookie is out of reach of anything that did run', async () => {
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    const session = (await page.cookies()).find((c) => c.name.toLowerCase().includes('session'))
    assert.ok(session, 'there is no session cookie to protect')
    assert.ok(session.httpOnly, 'the session cookie is readable by script')
    assert.equal(session.sameSite, 'Lax', 'the session cookie is sent on cross-site requests')

    const visible = await page.evaluate(() => document.cookie)
    assert.ok(!visible.includes(session.value), `document.cookie carries the session: ${visible}`)
  })
})
