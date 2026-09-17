// A real drive, a real browser, no mocks below the network. These are the
// acceptance checks for the interface, and an interface that only works against
// a fake has not been checked at all.
//
// The browser is whatever Chrome the machine already has: puppeteer-core drives
// it and downloads nothing. Set CHROME to point at another one.

import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import puppeteer from 'puppeteer-core'

const REPO = fileURLToPath(new URL('../..', import.meta.url))

const CHROMES = [
  process.env.CHROME,
  '/usr/bin/google-chrome-stable',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
].filter(Boolean)

export function chromePath() {
  return CHROMES.find((p) => existsSync(p))
}

// build compiles the binary with the current interface embedded. Running the
// tests against a stale binary would check a version of the UI that is not the
// one in the working tree.
function build(binary) {
  return new Promise((resolve, reject) => {
    const go = spawn(join(process.env.HOME, '.local/go/bin/go'), ['build', '-o', binary, './cmd/drive'], {
      cwd: REPO,
      stdio: ['ignore', 'inherit', 'inherit'],
    })
    go.on('error', reject)
    go.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`go build exited ${code}`))))
  })
}

// freePort borrows a port from the kernel and gives it straight back, so
// concurrent runs of this file do not collide. drive itself refuses port 0 —
// a configured address it cannot report back is not a configured address.
function freePort() {
  return new Promise((resolve, reject) => {
    const probe = createServer()
    probe.on('error', reject)
    probe.listen(0, '127.0.0.1', () => {
      const { port } = probe.address()
      probe.close(() => resolve(port))
    })
  })
}

/**
 * start brings up a drive on a free port over a fresh data directory and hands
 * back its base URL, its first-run setup token, and the directory its files are
 * in — which the tests read to check that what the interface says happened on
 * disk actually happened on disk.
 */
export async function start() {
  const dir = mkdtempSync(join(tmpdir(), 'drive-e2e-'))
  const binary = join(dir, 'drive')
  await build(binary)

  const proc = spawn(binary, [], {
    env: { ...process.env, DRIVE_DATA_DIR: join(dir, 'data'), DRIVE_ADDR: `127.0.0.1:${await freePort()}` },
    stdio: ['ignore', 'pipe', 'pipe'],
  })

  const startup = await new Promise((resolve, reject) => {
    let log = ''
    const timer = setTimeout(() => reject(new Error(`drive did not start:\n${log}`)), 30000)
    const onData = (chunk) => {
      log += chunk
      const address = log.match(/address=(\S+)/)
      const token = log.match(/token=([\w-]+)/)
      if (address && token) {
        clearTimeout(timer)
        resolve({ base: `http://${address[1]}`, token: token[1] })
      }
    }
    proc.stderr.on('data', onData)
    proc.stdout.on('data', onData)
    proc.on('exit', (code) => reject(new Error(`drive exited ${code}:\n${log}`)))
  })

  return {
    ...startup,
    // tmpDir is scratch space outside the data directory, for the source files
    // a test uploads. Putting them in the data directory would hand them to the
    // reconciler as well.
    tmpDir: dir,
    dataDir: join(dir, 'data'),
    userDir: (username) => join(dir, 'data', 'users', username),
    stop() {
      proc.kill('SIGTERM')
      rmSync(dir, { recursive: true, force: true })
    },
  }
}

export async function browser() {
  return puppeteer.launch({
    executablePath: chromePath(),
    headless: true,
    // The test container has no user namespaces to spare, and there is no
    // untrusted content here: every page is one this repository built.
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  })
}

// text waits for a selector and returns its text, which is most of what these
// assertions look at.
export const text = async (page, selector) =>
  page.$eval(selector, (el) => el.textContent.trim()).catch(() => '')

// clickText clicks the one element matching selector whose text contains label.
// Buttons here are labelled words, not test ids: if the label is wrong the test
// should fail, because a user would not find the button either.
export async function clickText(page, selector, label) {
  const found = await page.evaluateHandle(
    (sel, want) => [...document.querySelectorAll(sel)].find((el) => el.textContent.includes(want)),
    selector,
    label,
  )
  const element = found.asElement()
  if (!element) throw new Error(`no ${selector} labelled ${JSON.stringify(label)}`)
  await element.click()
}

// setup completes the first-run flow through the interface and leaves the page
// signed in as the new administrator.
export async function signUp(page, drive, username, password) {
  await page.goto(`${drive.base}/setup?token=${drive.token}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('input[autocomplete="username"]')
  await page.type('input[autocomplete="username"]', username)
  await page.type('input[type="password"]', password)
  await Promise.all([page.waitForSelector('header nav'), clickText(page, 'button', 'Create account')])
}
