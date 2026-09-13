import { CheckCircleFilled, ClockCircleOutlined, CloseCircleFilled, LoadingOutlined, PauseCircleOutlined, WarningFilled } from '@ant-design/icons'
import { Alert, Badge, Collapse, Empty, Tag, Timeline, Typography } from 'antd'
import type { MigrationEvent, MigrationStep, StepTimeline } from '../api/client'

const { Text } = Typography
const labels: Record<MigrationStep['type'], string> = {
  PREFLIGHT: '执行期检查', PRESYNC: '在线预同步', QUIESCE: '停止源业务', FINAL_BACKUP: '最终备份', TRANSFER: '数据传输',
  TRANSFORM: '资源转换与映射', RESTORE: '目标恢复', VALIDATION: '业务验证', AWAIT_CUTOVER: '等待人工切流', ROLLBACK: '恢复源业务',
}

export default function MigrationExecutionTimeline({ value, fallbackEvents = [], loading }: { value?: StepTimeline; fallbackEvents?: MigrationEvent[]; loading?: boolean }) {
  if (!value && !loading) return <Empty description="尚未形成执行时序证据" />
  const diagnosis = value?.diagnosis
  const steps = (value?.steps ?? []).map((item) => ({ ...item, attempts: item.attempts ?? [], events: item.events ?? [] }))
  const diagnosisType = diagnosis?.state === 'FAILED' || diagnosis?.state === 'STALLED' ? 'error' : diagnosis?.state === 'RETRYING' ? 'warning' : diagnosis?.state === 'RUNNING' || diagnosis?.state === 'WAITING' ? 'info' : 'success'
  return <div className="execution-timeline">
    {diagnosis && <Alert className="diagnosis-alert" showIcon type={diagnosisType} title={diagnosis.title} description={<>
      {diagnosis.reason && <div>{diagnosis.reason}</div>}
      {diagnosis.lastSuccessfulStep && <div>最后成功步骤：{diagnosis.lastSuccessfulStep}</div>}
      {diagnosis.remediation && <div><strong>建议：</strong>{diagnosis.remediation}</div>}
    </>} />}
    <Timeline pending={loading ? '正在读取执行证据…' : undefined} items={steps.map((item) => ({
      color: stepColor(item.step.status),
      icon: stepIcon(item.step.status),
      content: <div className="execution-step">
        <div className="execution-step-head"><strong>{labels[item.step.type]}</strong><Tag color={stepColor(item.step.status)}>{item.step.status}</Tag><span>{item.step.progress}%</span></div>
        <div className="execution-step-time">{item.step.startedAt ? formatTime(item.step.startedAt) : '尚未开始'}{item.step.completedAt ? ` → ${formatTime(item.step.completedAt)} · ${duration(item.step.startedAt, item.step.completedAt)}` : ''}</div>
        {item.step.summary && <div className="execution-step-summary">{item.step.summary}</div>}
        {(item.attempts.length > 0 || item.events.length > 0) && <Collapse ghost size="small" items={[{
          key: item.step.id,
          label: `${item.attempts.length} 次尝试 · ${item.events.length} 条关键事件`,
          children: <>
            {item.attempts.map((attempt) => <div className="attempt-row" key={`${attempt.stepId}-${attempt.attempt}`}>
              <Badge status={attempt.status === 'SUCCEEDED' ? 'success' : attempt.status === 'FAILED' ? 'error' : attempt.status === 'RETRY_SCHEDULED' ? 'warning' : 'processing'} />
              <strong>第 {attempt.attempt} 次</strong><Tag>{attempt.status}</Tag><span>{formatTime(attempt.startedAt)} · {duration(attempt.startedAt, attempt.completedAt)}</span>
              {attempt.heartbeatAt && <Text type="secondary">最后心跳 {formatTime(attempt.heartbeatAt)}</Text>}
              {attempt.nextAttemptAt && <Text type="warning">下次重试 {formatTime(attempt.nextAttemptAt)}</Text>}
              {attempt.errorMessage && <Text type="danger">{attempt.errorMessage}</Text>}
              {attempt.diagnostic && <Text type="secondary">{attempt.diagnostic}</Text>}
            </div>)}
            <EventRows events={item.events} />
          </>,
        }]} />}
      </div>,
    }))} />
    <h3 className="timeline-events-title">关键日志时间线</h3>
    <EventRows events={value?.events?.length ? value.events : fallbackEvents} />
  </div>
}

function EventRows({ events }: { events?: MigrationEvent[] | null }) {
  if (!events?.length) return null
  return <div className="timeline-event-rows">{events.map((event) => <div className="timeline-event-row" key={event.id}>
    <Badge status={event.severity === 'ERROR' ? 'error' : event.severity === 'WARNING' ? 'warning' : 'processing'} />
    <time>{formatTime(event.createdAt)}</time><Tag>{event.type}</Tag><span>{event.message}</span>
  </div>)}</div>
}

function stepColor(status: MigrationStep['status']) { return status === 'SUCCEEDED' ? 'green' : status === 'FAILED' ? 'red' : status === 'RUNNING' ? 'blue' : 'gray' }
function stepIcon(status: MigrationStep['status']) {
  if (status === 'SUCCEEDED') return <CheckCircleFilled />
  if (status === 'FAILED') return <CloseCircleFilled />
  if (status === 'RUNNING') return <LoadingOutlined spin />
  if (status === 'SKIPPED') return <WarningFilled />
  if (status === 'PENDING') return <ClockCircleOutlined />
  return <PauseCircleOutlined />
}
function formatTime(value?: string) { return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) : '—' }
function duration(start?: string, end?: string) {
  if (!start) return '—'
  const seconds = Math.max(0, Math.floor((new Date(end ?? Date.now()).getTime() - new Date(start).getTime()) / 1000))
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}
