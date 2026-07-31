// Records the Signal console end-to-end walkthrough (docs/assets/ui-walkthrough.*).
//
// Prereqs:
//   cd frontend && npm ci && npm run build && npm run start   # serves :3001
//   npm i -D playwright                                       # uses system Chrome
//
// Run:
//   node scripts/demo/record-ui.mjs ./out
//   # then convert the produced .webm:
//   V=$(ls out/*.webm | head -1)
//   ffmpeg -i "$V" -vf scale=1280:800:flags=lanczos -c:v libx264 -crf 20 \
//     -pix_fmt yuv420p -movflags +faststart docs/assets/ui-walkthrough.mp4
//   ffmpeg -i "$V" -vf "fps=12,scale=960:-1:flags=lanczos,palettegen=stats_mode=diff" palette.png
//   ffmpeg -i "$V" -i palette.png -lavfi \
//     "fps=12,scale=960:-1:flags=lanczos[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=3" \
//     docs/assets/ui-walkthrough.gif

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const OUT = process.argv[2] || './out'
const URL = process.env.SIGNAL_URL || 'http://localhost:3001'
const W = 1440, H = 900
mkdirSync(OUT, { recursive: true })
const wait = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await chromium.launch({ channel: 'chrome', headless: true })
const ctx = await browser.newContext({
  viewport: { width: W, height: H },
  deviceScaleFactor: 2,
  recordVideo: { dir: OUT, size: { width: W, height: H } },
})
const page = await ctx.newPage()

const sendBtn = () => page.getByRole('button', { name: 'Send', exact: true })
const openFav = (name) => page.locator('aside').getByText(name, { exact: true }).first().click()
async function sendAndAwaitResponse(holdMs) {
  await sendBtn().click()
  await page.locator('text=200 OK').first().waitFor({ timeout: 9000 }).catch(() => {})
  await wait(holdMs)
}

await page.goto(URL, { waitUntil: 'networkidle' })
await wait(2000) // hero: backdrop, service tree (8 methods), panels settle

// 1) Unary — Get user
await openFav('Get user')
await wait(900)
await sendAndAwaitResponse(2800)

// 2) Unary — Create user (mutation)
await openFav('Create user')
await wait(900)
await sendAndAwaitResponse(2800)

// 3) Server streaming — Stream events (equalizer + live messages)
await openFav('Stream events')
await wait(900)
await sendBtn().click()
await page.locator('text=MESSAGE 3').first().waitFor({ timeout: 9000 }).catch(() => {})
await wait(3800)

await wait(1500)
await ctx.close() // finalizes the .webm
await browser.close()
console.log('done')
