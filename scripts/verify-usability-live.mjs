// Explicit live API regression. Reads credential locally, never prints it.
// Saves a source resource selection; does not scale or migrate any workload.
import { readFile } from 'node:fs/promises'
import assert from 'node:assert/strict'

const base = process.env.PLATFORM_URL ?? 'http://192.168.118.206:31500'
const password = (await readFile(process.env.ADMIN_PASSWORD_FILE ?? new URL('../.data/secrets/admin-password', import.meta.url), 'utf8')).trim()
const cookies = new Map()
async function api(path, method = 'GET', body) {
  const response = await fetch(`${base}/api/v1${path}`, {
    method, headers: { 'Content-Type': 'application/json', Cookie: [...cookies].map(([k,v]) => `${k}=${v}`).join('; '), 'X-CSRF-Token': cookies.get('sks_migration_csrf') ?? '' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(120000),
  })
  for (const cookie of response.headers.getSetCookie()) {
    const [key, ...value] = cookie.split(';')[0].split('='); cookies.set(key, value.join('='))
  }
  const value = response.status === 204 ? null : await response.json()
  return { status: response.status, value }
}
assert.equal((await api('/auth/login', 'POST', { username: 'admin', password })).status, 204, 'login failed')
const { value: environments } = await api('/environments')
const source = environments.find(e => e.name === 'sida' && e.role === 'SOURCE')
assert.ok(source, 'sida source not found')
const inventory = await api('/applications/preview', 'POST', { environmentId: source.id, namespace: 'iwei-order' })
assert.equal(inventory.status, 200)
const resources = inventory.value.resources.filter(r => ['order-web', 'order-worker', 'order-web-content'].includes(r.name)).map(({ apiVersion, kind, namespace, name }) => ({ apiVersion, kind, namespace, name }))
assert.ok(resources.length >= 3)
const selection = await api('/applications/discover', 'POST', { environmentId: source.id, namespace: 'iwei-order', name: 'iwei-order-ui-regression', resources })
assert.equal(selection.status, 200, 'manual selection registration failed')
console.log('manual selection: passed, resources=' + selection.value.inventory.resources.length)
const harbor = environments.find(e => e.name === 'harbor')
if (harbor) {
  const discovery = await api('/applications/discover-compose', 'POST', { environmentId: harbor.id })
  if (discovery.status === 200) {
    assert.ok(discovery.value.length > 0, 'Harbor silently returned zero projects')
    console.log('Harbor discovery: projects=' + discovery.value.length)
  } else {
    assert.equal(discovery.status, 422)
    assert.match(discovery.value.detail, /权限/)
    console.log('Harbor discovery: explicit configuration-permission error (not false success)')
  }
}
console.log('No source workloads changed; saved selection is available in the wizard.')
