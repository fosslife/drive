// Capture the interface from the real binary in a real browser, with content
// dense enough to judge: nested folders, long filenames, photographs with real
// thumbnails, a selection, an upload in flight, and every screen.
//
// Not a test — this is the evidence a design review reads. It drives the same
// binary and the same Chrome the acceptance suite does.
//
//   node web/e2e/shots.mjs [outdir] [width]

import { execFileSync } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import puppeteer from 'puppeteer-core'

import { chromePath, start } from './harness.js'

const out = process.argv[2] || '.impeccable/review'
const width = Number(process.argv[3] || 1440)
const tag = process.argv[4] || (width <= 500 ? 'mobile' : 'desktop')
mkdirSync(out, { recursive: true })

const FOLDERS = ['Photographs', 'Scans', 'Correspondence']

// Real rasters, so the thumbnailer has something to actually decode and the
// contact prints in the reference column are real photographs of something.
const PHOTOS = [
  ['jokulsarlon-glacier-lagoon-at-dawn.png', 'gradient:#1b3a5c-#c9a978'],
  ['reynisfjara-basalt-columns.png', 'gradient:#2b2b2e-#8d8f94'],
  ['vestrahorn-black-sand-reflection.png', 'gradient:#0f1a14-#5c7a63'],
  ['seljalandsfoss-from-behind-the-fall.png', 'gradient:#3f5a6b-#dfe8ea'],
  ['hraunfossar-in-october.png', 'gradient:#6b4a2b-#e3c9a1'],
  ['kirkjufell-long-exposure.png', 'gradient:#1d3b2e-#b4914f'],
]

const DOCS = [
  'tax-return-2024-final-signed.pdf',
  'house-survey-report-full.pdf',
  'recipe-for-my-mothers-christmas-cake.txt',
  'car-insurance-renewal-quote-comparison.pdf',
  'backup-of-old-phone-2019.tar.gz',
  'wedding-speech-draft-seven.txt',
  'boiler-service-certificate.pdf',
  'notes-on-the-drive-south.txt',
]

async function main() {
  const exe = chromePath()
  if (!exe) throw new Error('no Chrome on this machine')
  const drive = await start()
  const browser = await puppeteer.launch({
    executablePath: exe,
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--force-color-profile=srgb'],
  })

  try {
    const page = await browser.newPage()
    await page.setViewport({ width, height: width <= 500 ? 844 : 900, deviceScaleFactor: 2 })

    // Every naming operation in this interface is a native prompt. Answer them
    // from a queue so the capture run never blocks on one.
    const answers = []
    await page.exposeFunction('__nextAnswer', () => answers.shift() ?? null)
    await page.evaluateOnNewDocument(() => {
      window.prompt = () => {
        // eslint-disable-next-line no-undef
        return window.__pending ?? null
      }
      window.confirm = () => true
    })

    await page.goto(`${drive.base}/setup?token=${drive.token}`, { waitUntil: 'networkidle0' })
    await page.type('input[autocomplete="username"]', 'anna')
    await page.type('input[type="password"]', 'a-long-enough-passphrase')
    await shot(page, `${tag}-gate`)

    await Promise.all([page.waitForSelector('header nav'), clickText(page, 'button', 'Create account')])

    // Folders, through the interface's own prompt.
    for (const name of FOLDERS) {
      await page.evaluate((n) => (window.__pending = n), name)
      await clickText(page, '.toolbar button', 'New folder')
      await page.waitForFunction((n) => [...document.querySelectorAll('.row .name')].some((e) => e.textContent === n), {}, name)
    }

    // Files, through the real picker and the real resumable upload.
    const sources = []
    for (const [name, spec] of PHOTOS) {
      const p = join(drive.tmpDir, name)
      execFileSync('magick', ['-size', '1200x800', spec, '-blur', '0x14', p])
      sources.push(p)
    }
    for (const name of DOCS) {
      const p = join(drive.tmpDir, name)
      writeFileSync(p, 'x'.repeat(4096 + Math.floor(Math.random() * 900000)))
      sources.push(p)
    }

    const picker = await page.$('input[type="file"]')
    await picker.uploadFile(...sources)
    await page.waitForFunction(() => !document.querySelector('.uploads'), { timeout: 120000 })
    await page.waitForFunction(() => document.querySelectorAll('.row').length >= 16, { timeout: 30000 })
    await settleThumbs(page)
    await shot(page, tag)

    // Photographs, inside the folder that gets shared, so the page a stranger
    // sees is a real inventory rather than an empty one.
    await clickRow(page, 'Photographs')
    await page.waitForFunction(() => location.pathname.includes('Photographs'))
    const inner = await page.$('input[type="file"]')
    await inner.uploadFile(...sources.slice(0, PHOTOS.length))
    await page.waitForFunction(() => !document.querySelector('.uploads'), { timeout: 120000 })
    await settleThumbs(page)
    await shot(page, `${tag}-folder`)
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('.row')

    // A selection: the notes column's live extent, and the operations lit.
    await page.click('.row')
    await page.keyboard.down('Shift')
    const rows = await page.$$('.row')
    await rows[5].click()
    await page.keyboard.up('Shift')
    await shot(page, `${tag}-selected`)

    // The plate.
    await clickRow(page, 'kirkjufell')
    await page.waitForSelector('.viewer img')
    await page.waitForFunction(() => {
      const i = document.querySelector('.viewer img')
      return i && i.complete && i.naturalWidth > 0
    })
    await shot(page, `${tag}-viewer`)
    await page.keyboard.press('Escape')

    // Search across the whole collection.
    await page.type('.search input', 'iceland')
    await page.keyboard.press('Enter')
    await page.waitForFunction(() => location.search.includes('q='))
    await new Promise((r) => setTimeout(r, 600))
    await shot(page, `${tag}-search`)

    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })

    // The other three series.
    await clickText(page, 'header nav a', 'Trash')
    await page.waitForSelector('.panel')
    await shot(page, `${tag}-trash`)

    await clickText(page, 'header nav a', 'API tokens')
    await page.waitForSelector('.panel')
    await page.evaluate(() => (window.__pending = 'backup script on the NAS'))
    await clickText(page, '.toolbar button', 'New token')
    await page.waitForSelector('.card input[readonly]')
    await shot(page, `${tag}-tokens`)

    // A share link, and the page a stranger sees when they follow it.
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('.row')
    await clickRow(page, 'Photographs', { open: false })
    await clickText(page, '.toolbar button', 'Share')
    await page.waitForSelector('.viewer form.card')
    await shot(page, `${tag}-share-dialog`)
    await clickText(page, '.card button', 'Create link')
    await page.waitForSelector('.card input[readonly]')
    const url = await page.$eval('.card input[readonly]', (el) => el.value)

    const visitor = await browser.newPage()
    await visitor.setViewport({ width, height: width <= 500 ? 844 : 900, deviceScaleFactor: 2 })
    await visitor.goto(url, { waitUntil: 'networkidle0' })
    await visitor.waitForSelector('.panel')
    await shot(visitor, `${tag}-shared`)

    // The same document after hours.
    await page.emulateMediaFeatures([{ name: 'prefers-color-scheme', value: 'dark' }])
    await page.goto(`${drive.base}/`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('.row')
    await settleThumbs(page)
    await shot(page, `${tag}-dark`)
  } finally {
    await browser.close()
    drive.stop()
  }
}

async function settleThumbs(page) {
  await page.waitForFunction(
    () => [...document.querySelectorAll('.row img.icon')].every((i) => i.complete),
    { timeout: 30000 },
  )
}

async function clickRow(page, name, { open = true } = {}) {
  const row = await page.evaluateHandle(
    (n) => [...document.querySelectorAll('.row')].find((r) => r.querySelector('.name')?.textContent.includes(n)),
    name,
  )
  const el = row.asElement()
  if (!el) throw new Error(`no row named ${name}`)
  await el.click()
  if (open) await el.click({ count: 2 })
}

async function clickText(page, selector, label) {
  const handle = await page.evaluateHandle(
    (sel, want) => [...document.querySelectorAll(sel)].find((el) => el.textContent.includes(want)),
    selector,
    label,
  )
  const el = handle.asElement()
  if (!el) throw new Error(`no ${selector} containing ${label}`)
  await el.click()
}

async function shot(page, name) {
  await page.evaluate(() => document.fonts.ready)
  await new Promise((r) => setTimeout(r, 400))
  await page.screenshot({ path: join(out, `${name}.png`) })
  console.log('wrote', name)
}

main().catch((e) => {
  console.error(e)
  process.exit(1)
})
