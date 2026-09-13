import { afterEach, expect, it, vi } from 'vitest'
import { discoverSelectedKubernetesApplication } from './client'

afterEach(() => vi.unstubAllGlobals())

it('sends only resource references when saving a manual selection', async () => {
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) })
  vi.stubGlobal('fetch', fetchMock)
  const resource = { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'shop', name: 'web', images: ['nginx'], labels: { app: 'web' } }
  await discoverSelectedKubernetesApplication('environment', 'shop', 'selected', [resource])
  const body = JSON.parse(fetchMock.mock.calls[0][1].body)
  expect(body.resources).toEqual([{ apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'shop', name: 'web' }])
})
