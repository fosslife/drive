// The acceptance checks for section 13, run against a real drive in a real
// browser. One process and one browser are shared by the whole file: starting
// them costs seconds, and nothing here needs isolation from the rest.
//
// The tests run in order and build on each other's state, the way a session
// does. A test that needs something creates it.

import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'

import { browser, chromePath, clickText, signUp, start, text } from './harness.js'

const USER = 'ada'
const PASSWORD = 'correct horse battery staple'

// A valid 64×64 PNG, so the thumbnailer has something real to decode.
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAVElEQVR4nOzPMQ3AQBTFsAyPP4ZCLYob' +
    'vuQoBLz6VpdfpwMAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB4D/gH' +
    'ALs/A5lsvpr/AAAAAElFTkSuQmCC',
  'base64',
)

// The virtualisation check would take minutes to set up honestly on disk, and
// it is about what the browser does with a big answer rather than about the
// server, so the answer is stubbed and the drive behind it left alone.
const fakeEntries = (n) =>
  Array.from({ length: n }, (_, i) => ({
    id: i + 1,
    name: `file-${String(i).padStart(6, '0')}.txt`,
    path: `file-${String(i).padStart(6, '0')}.txt`,
    kind: 'file',
    size: 1024 + i,
    modified: '2026-01-01T00:00:00Z',
    etag: `"${i + 1}-1-1024"`,
  }))

// stub answers one API path from the test instead of from the server.
// Everything else goes through to the real drive.
async function stub(page, match, body, status = 200) {
  const handler = (request) => {
    if (request.url().includes(match)) {
      request.respond({ status, contentType: 'application/json', body: JSON.stringify(body) })
    } else {
      request.continue()
    }
  }
  await page.setRequestInterception(true)
  page.on('request', handler)
  return async () => {
    page.off('request', handler)
    await page.setRequestInterception(false)
  }
}

describe('the web interface', { skip: chromePath() ? false : 'no Chrome on this machine' }, () => {
  let drive
  let chrome
  let page

  before(async () => {
    drive = await start()
    chrome = await browser()
    page = await chrome.newPage()
    page.setDefaultTimeout(20000)
    // An unanswered dialog hangs the page. These are the confirm() prompts on
    // destructive actions, and the tests mean to go through with them.
    page.on('dialog', (d) => d.accept(d.defaultValue?.() ?? ''))
  })

  after(async () => {
    await chrome?.close()
    drive?.stop()
  })

  const home = async () => {
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('header nav')
  }

  const go = async (label) => {
    await clickText(page, 'header nav a', label)
    await page.waitForSelector('.panel, .browser')
  }

  // upload puts files through the interface's own file picker, which is the
  // same code path a drop uses.
  const upload = async (names, contents) => {
    const paths = names.map((name, i) => {
      const file = join(drive.tmpDir, name)
      writeFileSync(file, contents[i])
      return file
    })
    const picker = await page.$('input[type="file"]')
    await picker.uploadFile(...paths)
    await page.waitForFunction(() => !document.querySelector('.uploads'), { timeout: 120000 })
    // The queue emptying and the folder refreshing are two different moments.
    // Reloading collapses them, so a test that uploads then looks for the row
    // is not racing the listing that puts it there.
    await page.reload({ waitUntil: 'networkidle0' })
    await page.waitForSelector('.row')
  }

  // 13.2
  test('an operator completes setup, signs out, and signs back in', async () => {
    await signUp(page, drive, USER, PASSWORD)
    assert.match(await text(page, 'header .who'), new RegExp(USER))
    assert.ok(existsSync(drive.userDir(USER)), 'the account has no storage root on disk')

    await Promise.all([page.waitForSelector('form.card'), clickText(page, 'header button', 'Sign out')])

    // The setup link is spent: it was the credential for exactly one account.
    await page.goto(`${drive.base}/setup?token=${drive.token}`, { waitUntil: 'networkidle0' })
    assert.match(await text(page, '.card h1'), /closed/i)

    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('input[autocomplete="username"]')
    await page.type('input[autocomplete="username"]', USER)
    await page.type('input[type="password"]', PASSWORD)
    await Promise.all([page.waitForSelector('header nav'), clickText(page, 'button', 'Sign in')])
    assert.match(await text(page, 'header .who'), new RegExp(USER))
  })

  // 13.3
  test('a folder is created, renamed, and deleted from the browser', async () => {
    await home()

    await page.evaluate(() => (window.prompt = () => 'Documents'))
    await clickText(page, '.toolbar button', 'New folder')
    await page.waitForFunction(() => document.body.textContent.includes('Documents'))
    assert.ok(existsSync(join(drive.userDir(USER), 'Documents')), 'no folder on disk')

    await clickRowNamed(page, 'Documents', { open: false })
    await page.evaluate(() => (window.prompt = () => 'Papers'))
    await clickText(page, '.toolbar button', 'Rename')
    await page.waitForFunction(() => document.body.textContent.includes('Papers'))
    assert.ok(existsSync(join(drive.userDir(USER), 'Papers')), 'the rename did not reach the disk')
    assert.ok(!existsSync(join(drive.userDir(USER), 'Documents')), 'the old name is still on disk')

    await clickRowNamed(page, 'Papers', { open: false })
    await clickText(page, '.toolbar button', 'Delete')
    await page.waitForFunction(() => !document.body.textContent.includes('Papers'))
    assert.ok(!existsSync(join(drive.userDir(USER), 'Papers')), 'still in the folder after deleting')
  })

  // A selection you cannot drop is a trap: the toolbar keeps offering to delete
  // something you have stopped pointing at.
  test('a selection is dropped by clicking past the rows, by Escape, and by ctrl-click', async () => {
    await home()
    await upload(['selectable.txt'], ['pick me\n'])

    const selectedCount = () => page.$$eval('.row.on', (r) => r.length)

    await clickRowNamed(page, 'selectable.txt', { open: false })
    assert.equal(await selectedCount(), 1, 'the row did not select')

    // Past the last row: the blank part of the scroll container.
    const box = await page.$eval('.rows', (el) => {
      const r = el.getBoundingClientRect()
      return { x: r.x + r.width / 2, y: r.bottom - 10 }
    })
    await page.mouse.click(box.x, box.y)
    await page.waitForFunction(() => document.querySelectorAll('.row.on').length === 0)

    await clickRowNamed(page, 'selectable.txt', { open: false })
    assert.equal(await selectedCount(), 1)
    await page.keyboard.press('Escape')
    await page.waitForFunction(() => document.querySelectorAll('.row.on').length === 0)

    // And ctrl-click toggles the row it lands on.
    await clickRowNamed(page, 'selectable.txt', { open: false })
    await page.keyboard.down('Control')
    await page.click('.row')
    await page.keyboard.up('Control')
    assert.equal(await selectedCount(), 0, 'ctrl-click did not deselect')
  })

  // Renaming used to offer the whole filename, so replacing it dropped the
  // extension and left an unopenable file behind.
  test('renaming a file keeps its extension', async () => {
    await home()
    await upload(['report.pdf'], ['not really a pdf\n'])

    await clickRowNamed(page, 'report.pdf', { open: false })
    // Capture what the prompt offers: that is what a user replaces wholesale.
    await page.evaluate(() => {
      window.__offered = null
      window.prompt = (_message, value) => {
        window.__offered = value
        return 'quarterly'
      }
    })

    await clickText(page, '.toolbar button', 'Rename')
    await page.waitForFunction(() => document.body.textContent.includes('quarterly.pdf'))
    assert.equal(
      await page.evaluate(() => window.__offered),
      'report',
      'the prompt offered the extension for editing',
    )
    assert.ok(existsSync(join(drive.userDir(USER), 'quarterly.pdf')), 'the renamed file is not on disk')
    assert.ok(!existsSync(join(drive.userDir(USER), 'quarterly')), 'the rename dropped the extension')
  })

  // 13.3: the virtualisation claim. 100,000 rows, and the list stays a screenful.
  test('a hundred thousand rows render as a screenful and scroll to the end', async () => {
    const release = await stub(page, '/api/list', { path: '', entries: fakeEntries(100_000), indexing: false })
    try {
      await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
      await page.waitForSelector('.row')

      const rendered = await page.$$eval('.row', (rows) => rows.length)
      assert.ok(rendered > 0 && rendered < 80, `${rendered} rows in the DOM; virtualisation is not working`)
      assert.match(await text(page, '.row .name'), /file-000000\.txt/)

      // Scrolling to the very bottom must show the last row, which is the part
      // a naive window calculation gets wrong.
      await page.$eval('.rows', (el) => el.scrollTo(0, el.scrollHeight))
      await page.waitForFunction(() => document.body.textContent.includes('file-099999.txt'))

      const afterScroll = await page.$$eval('.row', (rows) => rows.length)
      assert.ok(afterScroll < 80, `${afterScroll} rows after scrolling to the end`)
    } finally {
      await release()
    }
  })

  // 13.4
  test('an upload interrupted mid-flight resumes and stores the right bytes', async () => {
    await home()

    // Three chunks at the client's 8 MiB chunk size, so the interruption lands
    // between two of them and the resume has real ground to make up.
    const bytes = Buffer.alloc(20 << 20)
    for (let i = 0; i < bytes.length; i++) bytes[i] = (i * 31 + 7) & 0xff
    const source = join(drive.tmpDir, 'big-upload.bin')
    writeFileSync(source, bytes)

    let cutAfter = 1
    let killed = false
    const cut = (request) => {
      if (request.method() === 'PATCH' && request.url().includes('/api/uploads/')) {
        if (cutAfter-- === 0) {
          killed = true
          request.abort('connectionfailed')
          return
        }
      }
      request.continue()
    }
    await page.setRequestInterception(true)
    page.on('request', cut)

    try {
      const picker = await page.$('input[type="file"]')
      await picker.uploadFile(source)
      // Progress is per file and visible while it runs.
      await page.waitForSelector('.upload progress')
      await page.waitForFunction(() => !document.querySelector('.uploads'), { timeout: 120000 })
    } finally {
      page.off('request', cut)
      await page.setRequestInterception(false)
    }

    assert.ok(killed, 'the test never interrupted the upload')
    const stored = join(drive.userDir(USER), 'big-upload.bin')
    assert.ok(existsSync(stored), 'the resumed upload never landed')
    assert.ok(readFileSync(stored).equals(bytes), 'the resumed upload stored different bytes')
  })

  // 13.5 and 13.11
  test('thumbnails load without gating the list, and an image opens in place', async () => {
    await home()
    await upload(['one.png', 'two.png'], [PNG, PNG])
    await page.waitForFunction(() => document.querySelectorAll('.row img.icon').length >= 2)

    // The rows are there whether or not any thumbnail has arrived: the image is
    // lazy and asynchronous, and nothing waits on it.
    const lazy = await page.$$eval('.row img.icon', (imgs) => imgs.map((i) => i.loading))
    assert.ok(lazy.length >= 2, `expected thumbnails, found ${lazy.length}`)
    assert.ok(
      lazy.every((l) => l === 'lazy'),
      `thumbnails are not lazy: ${lazy}`,
    )
    // And they really are pictures of the files, produced by the server.
    await page.waitForFunction(
      () => [...document.querySelectorAll('.row img.icon')].some((i) => i.naturalWidth > 0),
      { timeout: 30000 },
    )

    // 13.11: open one and walk to the next with the arrow key.
    await clickRowNamed(page, 'one.png')
    await page.waitForSelector('.viewer img')
    assert.match(await text(page, '.viewer-bar span'), /one\.png/)
    await page.keyboard.press('ArrowRight')
    await page.waitForFunction(() => document.querySelector('.viewer-bar span').textContent.includes('two.png'))
    await page.keyboard.press('Escape')
    await page.waitForFunction(() => !document.querySelector('.viewer'))
  })

  // 13.10, and the first half of 13.6
  test('fifty items are selected and deleted in one action', async () => {
    // Tall enough that all fifty rows are really on screen. The list is
    // virtualised, so at the default viewport only a screenful exists in the
    // DOM and "select the last row" would mean the last *rendered* row.
    await page.setViewport({ width: 1200, height: 2400 })
    await home()
    await page.evaluate(() => (window.prompt = () => 'bulk'))
    await clickText(page, '.toolbar button', 'New folder')
    await page.waitForFunction(() => document.body.textContent.includes('bulk'))
    await clickRowNamed(page, 'bulk')
    await page.waitForFunction(() => location.pathname.includes('/browse/'))

    const names = Array.from({ length: 50 }, (_, i) => `item-${String(i).padStart(2, '0')}.txt`)
    await upload(
      names,
      names.map((n) => `this is ${n}\n`),
    )
    await page.waitForFunction(() => document.querySelectorAll('.row').length === 50, { timeout: 120000 })

    await page.click('.row')
    const rows = await page.$$('.row')
    await page.keyboard.down('Shift')
    await rows.at(-1).click()
    await page.keyboard.up('Shift')
    const selected = await page.$$eval('.row.on', (r) => r.length)
    assert.equal(selected, 50, 'shift-click did not take the whole range')

    await clickText(page, '.toolbar button', 'Delete')
    await page.waitForFunction(() => document.querySelectorAll('.row').length === 0, { timeout: 120000 })
    assert.equal(readdirSync(join(drive.userDir(USER), 'bulk')).length, 0, 'the folder on disk still has files in it')

    await go('Trash')
    await page.waitForSelector('table tbody tr')
    // The trash also holds what earlier tests put there, so count the fifty
    // rather than the rows.
    const inTrash = await page.$$eval('table tbody tr td', (cells) =>
      cells.filter((c) => c.textContent.startsWith('bulk/item-')).length,
    )
    assert.equal(inTrash, 50, `${inTrash} of the fifty reached the trash`)

    await page.setViewport({ width: 800, height: 600 })
  })

  // 13.6
  test('a deleted file is restored, and permanent deletion really destroys it', async () => {
    await go('Trash')
    await page.waitForSelector('table tbody tr')

    const path = await text(page, 'table tbody tr td')
    await clickText(page, 'table tbody tr td.actions button', 'Restore')
    await page.waitForFunction((p) => !document.body.textContent.includes(p), {}, path)
    assert.ok(existsSync(join(drive.userDir(USER), path)), `${path} is not back on disk`)

    const doomed = await text(page, 'table tbody tr td')
    await clickText(page, 'table tbody tr td.actions button', 'Delete forever')
    await page.waitForFunction((p) => !document.body.textContent.includes(p), {}, doomed)
    assert.ok(!existsSync(join(drive.userDir(USER), doomed)), `${doomed} came back from a permanent delete`)
  })

  // 13.7
  test('a partial search term finds files and shows their paths', async () => {
    await home()
    await clickRowNamed(page, 'bulk')
    await page.waitForFunction(() => location.pathname.includes('/browse/'))
    await upload(['taxes.pdf'], ['a tax return\n'])

    await home()
    await page.type('.search input', 'tax')
    await page.keyboard.press('Enter')
    await page.waitForFunction(() => location.search.includes('q=tax'))
    // For the result, not merely for a row: the rows of the folder that was on
    // screen a moment ago are still there while the search is in flight.
    await page.waitForFunction(() =>
      [...document.querySelectorAll('.row .name')].some((r) => r.textContent === 'bulk/taxes.pdf'),
    )

    const names = await page.$$eval('.row .name', (rows) => rows.map((r) => r.textContent))
    assert.deepEqual(names, ['bulk/taxes.pdf'], 'a partial term did not return the match at its full path')
  })

  // 13.8
  test('a share link opens in a logged-out browser and stops working when revoked', async () => {
    await home()
    await clickRowNamed(page, 'one.png', { open: false })
    await clickText(page, '.toolbar button', 'Share')
    await page.waitForSelector('.card input[type="password"]')
    await page.type('.card input[type="password"]', 'hunter2')
    await clickText(page, '.card button', 'Create link')
    await page.waitForSelector('.card input[readonly]')
    const url = await page.$eval('.card input[readonly]', (el) => el.value)
    assert.match(url, /\/s\//)
    await clickText(page, '.card button', 'Done')

    // A separate browser context is a separate cookie jar: none of the
    // signed-in session comes with it.
    const guest = await chrome.createBrowserContext()
    const visitor = await guest.newPage()
    visitor.setDefaultTimeout(20000)
    try {
      await visitor.goto(url, { waitUntil: 'networkidle0' })
      await visitor.waitForSelector('input[type="password"]')
      assert.ok(
        !(await visitor.evaluate(() => document.body.textContent.includes('one.png'))),
        'the protected link disclosed the filename before the password',
      )
      await visitor.type('input[type="password"]', 'hunter2')
      await visitor.click('button')
      await visitor.waitForFunction(() => document.body.textContent.includes('one.png'))

      await go('Shares')
      await page.waitForSelector('table tbody tr')
      await clickText(page, 'table tbody tr td.actions button', 'Revoke')
      await page.waitForFunction(() => !document.querySelector('table tbody tr'))

      await visitor.reload({ waitUntil: 'networkidle0' })
      await visitor.waitForSelector('.error')
      assert.ok(
        !(await visitor.evaluate(() => document.body.textContent.includes('one.png'))),
        'the revoked link still serves its target',
      )
    } finally {
      await guest.close()
    }
  })

  // 13.9
  test('a token secret is shown once and never again', async () => {
    await go('API tokens')
    await page.evaluate(() => (window.prompt = () => 'laptop'))
    await clickText(page, '.toolbar button', 'New token')
    await page.waitForSelector('.card input[readonly]')
    const secret = await page.$eval('.card input[readonly]', (el) => el.value)
    assert.ok(secret.length > 20, `that is not a token: ${secret}`)

    await clickText(page, '.card button', 'I have copied it')
    await go('Trash')
    await go('API tokens')
    await page.waitForSelector('table tbody tr')
    assert.ok(
      !(await page.evaluate((s) => document.body.textContent.includes(s), secret)),
      'the secret is still on the page after revisiting the list',
    )
    assert.ok(await page.evaluate(() => document.body.textContent.includes('laptop')), 'the token is not listed')
  })

  // 13.12
  test('a running scan is visible and listings say they may be incomplete', async () => {
    const release = await stub(page, '/api/scan', {
      running: true,
      root: 'users/ada',
      seen: 12345,
      scans: 0,
      indexing: true,
    })
    try {
      await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
      await page.waitForSelector('.indexing')
      const banner = await text(page, '.indexing')
      assert.match(banner, /indexing/i)
      assert.match(banner, /12,345|12345/)
    } finally {
      await release()
    }

    // And the listing itself says so, so a short folder is not read as an empty
    // one while the reconciler is still working through the root.
    const releaseList = await stub(page, '/api/list', { path: '', entries: [], indexing: true })
    try {
      await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
      await page.waitForFunction(() => document.body.textContent.includes('Still indexing'))
    } finally {
      await releaseList()
    }
  })

  // 13.13
  test('a full disk, a stale precondition and a refused upload each name their cause', async () => {
    const cases = [
      { status: 507, body: { error: 'not enough free space' }, expect: /disk space/i },
      { status: 412, body: { error: 'the entry has changed since it was read' }, expect: /changed[\s\S]*reload/i },
    ]
    for (const c of cases) {
      await home()
      const release = await stub(page, '/api/folders', c.body, c.status)
      try {
        await page.evaluate(() => (window.prompt = () => 'anything'))
        await clickText(page, '.toolbar button', 'New folder')
        await page.waitForSelector('.error.bar')
        const message = await text(page, '.error.bar')
        assert.match(message, c.expect)
        assert.doesNotMatch(message, /^(error|failed|something went wrong)\.?$/i)
      } finally {
        await release()
      }
    }

    // A refused upload names the file and the reason, and stays on screen
    // rather than vanishing with the queue.
    await home()
    const release = await stub(page, '/api/uploads', { error: 'not enough free space' }, 507)
    try {
      const source = join(drive.tmpDir, 'refused.bin')
      writeFileSync(source, 'x'.repeat(1024))
      const picker = await page.$('input[type="file"]')
      await picker.uploadFile(source)
      await page.waitForSelector('.upload.error')
      const message = await text(page, '.upload.error')
      assert.match(message, /refused\.bin/)
      assert.match(message, /disk space/i)
    } finally {
      await release()
    }
  })
})

// clickRowNamed finds a row by its visible name and either selects it or opens
// it. Double click is how a folder is entered, which is what a user does.
async function clickRowNamed(page, name, { open = true } = {}) {
  const row = await page.evaluateHandle(
    (n) => [...document.querySelectorAll('.row')].find((r) => r.textContent.includes(n)),
    name,
  )
  const element = row.asElement()
  if (!element) throw new Error(`no row named ${name}`)
  await element.click()
  if (open) await element.click({ count: 2 })
}
