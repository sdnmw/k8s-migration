import { describe, expect, it } from 'vitest'
import type { MigrationRunSummary } from '../api/client'
import { toMigrationRow } from './presentation'

function run(overrides: Partial<MigrationRunSummary> = {}): MigrationRunSummary {
  return {
    id: 'run-1', migrationPlanId: 'plan-1', runNumber: 1, status: 'COMPLETED', progress: 100,
    createdAt: '2026-09-23T00:00:00Z', updatedAt: '2026-09-23T00:10:00Z',
    planName: 'demo', sourceType: 'COMPOSE', sourceEnvironmentName: 'source', targetEnvironmentName: 'target',
    applicationName: 'demo', applicationNamespace: 'demo',
    ...overrides,
  }
}

describe('migration presentation', () => {
  it('keeps a completed target migration successful when source restoration fails', () => {
    const row = toMigrationRow(run({ errorCode: 'SOURCE_RESTORE_FAILED', errorMessage: 'source port is occupied' }))
    expect(row.status).toBe('成功有告警')
    expect(row.rawStatus).toBe('COMPLETED')
  })

  it('shows an actual failed migration as requiring attention', () => {
    expect(toMigrationRow(run({ status: 'FAILED', progress: 55 })).status).toBe('需要处理')
  })
})
