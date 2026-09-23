import type { MigrationRunStatus, MigrationRunSummary } from '../api/client'

export type MigrationStatusLabel = '迁移中' | '等待切流' | '成功' | '成功有告警' | '源端已恢复' | '需要处理'

export type MigrationRow = {
  key: string
  name: string
  type: 'Kubernetes' | 'Docker Compose'
  source: string
  target: string
  application: string
  status: MigrationStatusLabel
  rawStatus: MigrationRunStatus
  progress: number
  data: string
  createdAt: string
  duration: string
}

export function toMigrationRow(value: MigrationRunSummary, now = new Date()): MigrationRow {
  return {
    key: value.id,
    name: value.runNumber > 1 ? `${value.planName} #${value.runNumber}` : value.planName,
    type: value.sourceType === 'COMPOSE' ? 'Docker Compose' : 'Kubernetes',
    source: value.sourceEnvironmentName,
    target: value.targetEnvironmentName,
    application: value.applicationNamespace || value.applicationName,
    status: statusLabel(value),
    rawStatus: value.status,
    progress: value.progress,
    data: formatBytes(value.bytesTransferred || value.bytesTotal),
    createdAt: formatDate(value.createdAt),
    duration: formatDuration(value.startedAt, value.completedAt, now),
  }
}

export function isActiveStatus(status: MigrationRunStatus) {
  return !['COMPLETED', 'FAILED', 'CANCELLED'].includes(status)
}

function statusLabel(value: MigrationRunSummary): MigrationStatusLabel {
  const status = value.status
  if (status === 'COMPLETED' && value.errorCode === 'SOURCE_RESTORE_FAILED') return '成功有告警'
  if (status === 'COMPLETED') return '成功'
  if (status === 'AWAITING_CUTOVER') return '等待切流'
  if (status === 'CANCELLED' && value.errorCode === 'SOURCE_RESTORE_REQUESTED') return '源端已恢复'
  if (status === 'FAILED' || status === 'CANCELLED') return '需要处理'
  return '迁移中'
}

function formatBytes(bytes = 0) {
  if (bytes <= 0) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / (1024 ** index)
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`
}

function formatDate(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
}

function formatDuration(startedAt: string | undefined, completedAt: string | undefined, now: Date) {
  if (!startedAt) return '—'
  const start = new Date(startedAt).getTime()
  const end = completedAt ? new Date(completedAt).getTime() : now.getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return '—'
  const seconds = Math.floor((end - start) / 1000)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainder = seconds % 60
  return [hours, minutes, remainder].map((part) => String(part).padStart(2, '0')).join(':')
}
