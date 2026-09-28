// Acceptance checks for the administration screens, against a real drive in a
// real browser. Like the other e2e file, the tests run in order and build on
// each other's state: the operator signs up, then administers, then the account
// they created signs in.

import assert from 'node:assert/strict'
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'

import { browser, chromePath, clickText, signUp, start, text } from './harness.js'

const OPERATOR = 'root'
const PASSWORD = 'correct horse battery staple'
const FAMILY = 'ada'
const FAMILY_PASSWORD = 'a different long password'
const CHANGED_PASSWORD = 'the password set from the card'

describe('administration', { skip: chromePath() ? false : 'no Chrome on this machine' }, () => {
  let drive
  let chrome
  let page

  before(async () => {
    drive = await start()
    chrome = await browser()
    page = await chrome.newPage()
    page.setDefaultTimeout(20000)
    page.on('dialog', (d) => d.accept(d.defaultValue?.() ?? ''))
    await signUp(page, drive, OPERATOR, PASSWORD)
  })

  after(async () => {
    await chrome?.close()
    drive?.stop()
  })

  const home = async () => {
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('header nav')
  }

  const accounts = async () => {
    await page.goto(`${drive.base}/accounts`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('.panel table')
  }

  // rowFor returns the text of the row for one account, which is where every
  // figure about it is. Rows are ordered by username, never by when they were
  // created, so nothing here may address one by position.
  const rowFor = (username) =>
    page.evaluate((who) => {
      const row = [...document.querySelectorAll('tbody tr')].find((tr) => tr.textContent.includes(who))
      return row ? row.textContent : ''
    }, username)

  // clickInRow presses a named button in one account's row.
  const clickInRow = async (username, label) => {
    const found = await page.evaluateHandle(
      (who, want) => {
        const row = [...document.querySelectorAll('tbody tr')].find((tr) => tr.textContent.includes(who))
        return row && [...row.querySelectorAll('button')].find((b) => b.textContent.includes(want))
      },
      username,
      label,
    )
    const element = found.asElement()
    if (!element) throw new Error(`no ${label} button in the row for ${username}`)
    await element.click()
  }

  // waitForRow waits until one account's row reads the way it should.
  const waitForRow = (username, pattern) =>
    page.waitForFunction(
      (who, source) => {
        const row = [...document.querySelectorAll('tbody tr')].find((tr) => tr.textContent.includes(who))
        return row && new RegExp(source).test(row.textContent)
      },
      {},
      username,
      pattern.source,
    )

  const signInAs = async (username, password) => {
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('input[autocomplete="username"]')
    await page.type('input[autocomplete="username"]', username)
    await page.type('input[type="password"]', password)
    await Promise.all([page.waitForSelector('header nav'), clickText(page, 'button', 'Sign in')])
  }

  const signOut = async () => {
    await page.waitForSelector('header button')
    await Promise.all([page.waitForSelector('form.card'), clickText(page, 'header button', 'Sign out')])
  }

  // 6.1, 6.3
  test('the operator creates an account and it can sign in to a drive of its own', async () => {
    await home()
    const series = await page.$$eval('header nav a', (links) => links.map((a) => a.textContent))
    assert.ok(
      series.some((label) => /Accounts/.test(label)),
      `the administrator has no Accounts series: ${series.join(', ')}`,
    )

    await accounts()
    await clickText(page, '.toolbar button', 'New account')
    await page.waitForSelector('form.card input[name="username"]')
    await page.type('form.card input[name="username"]', FAMILY)
    await page.type('form.card input[name="password"]', FAMILY_PASSWORD)
    await clickText(page, 'form.card button', 'Create account')
    await page.waitForFunction(
      (who) => [...document.querySelectorAll('tbody tr')].some((tr) => tr.textContent.includes(who)),
      {},
      FAMILY,
    )

    await signOut()
    await signInAs(FAMILY, FAMILY_PASSWORD)
    assert.match(await text(page, 'header .who'), new RegExp(FAMILY))
    await page.waitForSelector('.browser')
    const rows = await page.$$('.row')
    assert.equal(rows.length, 0, 'a new account opened onto somebody else’s files')
  })

  // 6.1: the series is an administrator's, and typing the address is not a way
  // around that.
  test('an ordinary account has no Accounts series and cannot reach it', async () => {
    const series = await page.$$eval('header nav a', (links) => links.map((a) => a.textContent))
    assert.ok(!series.some((label) => /Accounts/.test(label)), `an ordinary account was offered ${series.join(', ')}`)

    await page.goto(`${drive.base}/accounts`, { waitUntil: 'networkidle0' })
    assert.match(await text(page, '.centred'), /administrator/i)

    await signOut()
    await signInAs(OPERATOR, PASSWORD)
  })

  // 6.2
  test('every account is listed with what it holds', async () => {
    await accounts()
    const operator = await rowFor(OPERATOR)
    const family = await rowFor(FAMILY)

    assert.match(operator, /administrator/i, 'the administrator is not marked as one')
    assert.match(operator, /Active/)
    assert.doesNotMatch(family, /administrator/i)
    // A figure for what each account holds, and a count of items, on both rows.
    for (const row of [operator, family]) {
      assert.match(row, /\d+(\.\d+)?\s?(B|KB|MB|GB)/, `no extent figure in: ${row}`)
    }
  })

  // 6.2, 6.3: a limit is set and shown, and the account is disabled and enabled
  // again from the same row.
  test('a storage limit and a disabled state are set from the row', async () => {
    await accounts()
    await page.evaluate(() => (window.prompt = () => '1024'))
    await clickInRow(FAMILY, 'Limit')
    await waitForRow(FAMILY, /of 1.0 KB/)

    await clickInRow(FAMILY, 'Disable')
    await waitForRow(FAMILY, /Disabled/)

    await clickInRow(FAMILY, 'Enable')
    await waitForRow(FAMILY, /Active/)
  })

  // 6.4: the API refuses, and the screen says what it said rather than "failed".
  test('disabling your own account is refused with the reason on screen', async () => {
    await accounts()
    await clickInRow(OPERATOR, 'Disable')
    await page.waitForSelector('.error.bar')
    assert.match(await text(page, '.error.bar'), /own account/i)
    assert.match(await rowFor(OPERATOR), /Active/, 'the refused row changed anyway')
  })

  // 6.5
  test('the instance panel reports the drive and the rescan button runs one', async () => {
    await accounts()
    const notes = await text(page, '.notes')
    assert.match(notes, /free of/i, `no free space figure in the notes: ${notes}`)
    assert.match(notes, /Version/i)
    assert.match(notes, new RegExp(drive.dataDir.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))

    await clickText(page, '.toolbar button', 'Rescan')
    // Either it is still indexing or it already finished one; both are the
    // button having worked, and which one it is depends on a race with a scan
    // of an almost-empty drive.
    await page.waitForFunction(() => /Indexing now|Last indexed/.test(document.querySelector('.notes').textContent))
  })

  // 7.1, 7.2
  test('a user changes their own password from their card', async () => {
    await page.goto(`${drive.base}/account`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('form.card')
    assert.match(await text(page, '.notes'), new RegExp(OPERATOR))

    // The wrong current password is refused, said in place, and leaves the
    // session alone.
    await page.type('input[name="current_password"]', 'not the password')
    await page.type('input[name="new_password"]', 'a replacement password')
    await clickText(page, 'form.card button', 'Change password')
    await page.waitForSelector('.error.bar')
    assert.match(await text(page, '.error.bar'), /current password/i)
    assert.match(await text(page, 'header .who'), new RegExp(OPERATOR), 'a refused change signed the user out')
    assert.equal(
      await page.$eval('input[name="new_password"]', (el) => el.value),
      'a replacement password',
      'a refused change cleared the form',
    )

    // And the right one goes through, after which the new password is the one
    // that signs in.
    await page.$eval('input[name="current_password"]', (el) => (el.value = ''))
    await page.$eval('input[name="new_password"]', (el) => (el.value = ''))
    await page.type('input[name="current_password"]', PASSWORD)
    await page.type('input[name="new_password"]', CHANGED_PASSWORD)
    await clickText(page, 'form.card button', 'Change password')
    await page.waitForFunction(() => /signed out/i.test(document.querySelector('.panel').textContent))

    await signOut()
    await signInAs(OPERATOR, CHANGED_PASSWORD)
    assert.match(await text(page, 'header .who'), new RegExp(OPERATOR))
  })

  // 7.3: the refusal an account over its limit sees, in the notes beside the
  // listing it was uploading into.
  test('an upload past the limit says which limit', async () => {
    await accounts()
    await page.evaluate(() => (window.prompt = () => '10'))
    await clickInRow(OPERATOR, 'Limit')
    await waitForRow(OPERATOR, /of 10 B/)

    await home()
    const source = join(drive.tmpDir, 'too-big.bin')
    writeFileSync(source, 'x'.repeat(4096))
    const picker = await page.$('input[type="file"]')
    await picker.uploadFile(source)
    await page.waitForSelector('.upload.error')
    const message = await text(page, '.upload.error')
    assert.match(message, /quota/i, `the refusal does not name the limit: ${message}`)
    assert.doesNotMatch(message, /disk space/i, `an account limit was reported as a full disk: ${message}`)
  })
})
