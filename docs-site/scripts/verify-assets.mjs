import fs from 'node:fs'
import path from 'node:path'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const build = path.join(root, 'build')
const shots = JSON.parse(fs.readFileSync(path.join(root, 'src/data/guide-screenshots.json')))
assert(shots.length >= 40, 'At least 40 real screenshots are required')
assert(new Set(shots.map(s => s.id)).size === shots.length, 'Screenshot IDs must be unique')
for (const shot of shots) {
  const file = path.join(root, 'static/img/guide', shot.file)
  const bytes = fs.readFileSync(file)
  assert.equal(bytes.readUInt32BE(16), 1440, `${shot.file}: width`)
  assert.equal(bytes.readUInt32BE(20), 900, `${shot.file}: height`)
  assert(shot.action && shot.expected && shot.failure, `${shot.file}: incomplete instructions`)
}
function files(directory) {
  return fs.readdirSync(directory, {withFileTypes:true}).flatMap(entry => entry.isDirectory() ? files(path.join(directory, entry.name)) : [path.join(directory, entry.name)])
}
let pages = 0
for (const file of files(build).filter(file => file.endsWith('.html'))) {
  pages++
  const html = fs.readFileSync(file, 'utf8')
  assert(!/<(?:script|img)\b[^>]*src=["']https?:\/\//i.test(html), `${file}: external resource`)
  for (const link of html.matchAll(/<link\b[^>]*>/gi)) {
    if (/rel=["'](?:stylesheet|preload|prefetch|icon)["']/i.test(link[0])) assert(!/href=["']https?:\/\//i.test(link[0]), `${file}: external link resource`)
  }
  for (const match of html.matchAll(/(?:src|href)=["'](\/docs\/[^"'#?]*)["']/g)) {
    const resource = path.join(build, decodeURIComponent(match[1].slice('/docs/'.length)))
    assert(fs.existsSync(resource) || fs.existsSync(path.join(resource, 'index.html')), `Missing asset/link: ${match[1]} in ${file}`)
  }
}
assert(pages >= 25, 'Expected documentation chapters')
assert(files(build).some(file => /search-index.*\.json$/.test(file)), 'Local search index missing')
console.log(JSON.stringify({screenshots:shots.length,pages,externalStaticResources:0,result:'passed'}))
