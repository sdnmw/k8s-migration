import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { DeleteOutlined, DownloadOutlined, EditOutlined, LoadingOutlined, MoreOutlined, RedoOutlined, RollbackOutlined, StopOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App as AntApplication, Button, Card, Descriptions, Dropdown, Popconfirm, Progress, Space, Statistic, Table, Tabs, Tag, Typography, type MenuProps } from 'antd'
import {
  cancelMigrationRun, cleanupMigrationArtifacts, confirmMigrationCutover, getMigrationRun, getMigrationPlan, deleteMigrationRun, getMigrationTimeline, getMigrationTopology, listEnvironments, listMigrationVolumeTransfers, migrationEventSource, migrationReportURL, refreshMigrationTopology, restoreMigrationSource, retryMigrationRun, rollbackMigrationRun,
  type MigrationEvent, type MigrationRunSnapshot, type MigrationRunStatus, type MigrationStep, type StepTimeline, type TopologyEvidence, type VolumeTransfer,
} from '../api/client'
import PageHeader from '../components/PageHeader'
import MigrationExecutionTimeline from '../components/MigrationExecutionTimeline'
import MigrationTopology from '../components/MigrationTopology'

const { Text } = Typography
const statusLabels: Record<MigrationRunStatus, string> = {
  PENDING: '等待执行', PREFLIGHT: '执行检查', PRESYNC: '在线预同步', QUIESCE: '停止源业务', FINAL_BACKUP: '最终备份',
  TRANSFER: '数据传输', TRANSFORM: '资源转换', RESTORE: '目标恢复', VALIDATION: '业务验证', AWAITING_CUTOVER: '等待人工切流',
  ROLLING_BACK: '正在恢复源业务', COMPLETED: '已完成', FAILED: '失败', CANCELLED: '已取消',
}

export default function MigrationRunDetailPage({ preview = false }: { preview?: boolean }) {
  const params = useParams()
  const runID = preview ? previewSnapshot.run.id : (params.id ?? '')
  const navigate = useNavigate()
  const { modal } = AntApplication.useApp()
  const [searchParams, setSearchParams] = useSearchParams()
  const queryClient = useQueryClient()
  const [events, setEvents] = useState<MigrationEvent[]>(preview ? previewEvents : [])
  const [actionError, setActionError] = useState('')
  const [busy, setBusy] = useState(false)
  const query = useQuery({
    queryKey: ['migration-run', runID], queryFn: () => getMigrationRun(runID), enabled: !preview && !!runID,
    initialData: preview ? previewSnapshot : undefined,
  })
  const transfers = useQuery({
    queryKey: ['migration-volume-transfers', runID], queryFn: () => listMigrationVolumeTransfers(runID), enabled: !preview && !!runID,
    initialData: preview ? previewTransfers : undefined,
  })
  const topology = useQuery({
    queryKey: ['migration-topology', runID], queryFn: () => getMigrationTopology(runID), enabled: !preview && !!runID,
    initialData: preview ? previewTopology : undefined,
  })
  const timeline = useQuery({
    queryKey: ['migration-timeline', runID], queryFn: () => getMigrationTimeline(runID), enabled: !preview && !!runID,
    initialData: preview ? previewTimeline : undefined,
  })
  const [refreshingTopology, setRefreshingTopology] = useState(false)

  useEffect(() => {
    if (preview || !runID) return
    const source = migrationEventSource(runID)
    const receive = (raw: Event) => {
      const event = raw as MessageEvent<string>
      try {
        const value = JSON.parse(event.data) as MigrationEvent
        setEvents((current) => current.some((item) => item.id === value.id) ? current : [...current, value].sort((a, b) => a.id - b.id))
        void queryClient.invalidateQueries({ queryKey: ['migration-run', runID] })
        void queryClient.invalidateQueries({ queryKey: ['migration-topology', runID] })
        void queryClient.invalidateQueries({ queryKey: ['migration-timeline', runID] })
        if (value.type === 'VOLUME_TRANSFER_PROGRESS') void queryClient.invalidateQueries({ queryKey: ['migration-volume-transfers', runID] })
      } catch { /* ignore malformed stream frames */ }
    }
    source.addEventListener('migration', receive)
    return () => { source.removeEventListener('migration', receive); source.close() }
  }, [preview, queryClient, runID])

  async function cancel() {
    setBusy(true); setActionError('')
    try {
      const value = await cancelMigrationRun(runID)
      queryClient.setQueryData(['migration-run', runID], value)
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function retry() {
    setBusy(true); setActionError('')
    try {
      const value = await retryMigrationRun(runID)
      navigate(`/migrations/${value.run.id}`)
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function editAndRetry() {
    setBusy(true); setActionError('')
    try {
      const draft = await getMigrationPlan(query.data!.run.migrationPlanId)
      const environments = await listEnvironments()
      const source = environments.find(item => item.id === draft.sourceEnvironmentId)
      if (!source) throw new Error('原源环境已不存在，请重新创建迁移并选择有效源环境。')
      navigate('/migrations/new', { state: { draft, sourceType: source.kind === 'DOCKER_COMPOSE' ? 'COMPOSE' : 'KUBERNETES' } })
    } catch (error) { setActionError(errorMessage(error)) } finally { setBusy(false) }
  }
  async function removeRun() {
    setBusy(true); setActionError('')
    try { await deleteMigrationRun(runID); await queryClient.invalidateQueries({ queryKey: ['migration-runs'] }); navigate('/migrations') }
    catch (error) { setActionError(errorMessage(error)) } finally { setBusy(false) }
  }
  async function cutover() {
    setBusy(true); setActionError('')
    try {
      await confirmMigrationCutover(runID)
      await queryClient.invalidateQueries({ queryKey: ['migration-run', runID] })
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function rollback() {
    setBusy(true); setActionError('')
    try {
      const value = await rollbackMigrationRun(runID)
      queryClient.setQueryData(['migration-run', runID], value)
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function restoreSource() {
    setBusy(true); setActionError('')
    try {
      const value = await restoreMigrationSource(runID)
      queryClient.setQueryData(['migration-run', runID], value)
      await queryClient.invalidateQueries({ queryKey: ['migration-timeline', runID] })
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function cleanupArtifacts() {
    setBusy(true); setActionError('')
    try {
      await cleanupMigrationArtifacts(runID)
      await queryClient.invalidateQueries({ queryKey: ['migration-run', runID] })
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function refreshTopology() {
    if (preview) return
    setRefreshingTopology(true); setActionError('')
    try {
      const value = await refreshMigrationTopology(runID)
      queryClient.setQueryData(['migration-topology', runID], value)
    } catch (reason) { setActionError(errorMessage(reason)) } finally { setRefreshingTopology(false) }
  }

  if (query.isPending) return <Card className="section-card run-loading"><LoadingOutlined /><Text>正在读取迁移任务…</Text></Card>
  if (query.isError || !query.data) return <><PageHeader title="迁移任务" /><Alert showIcon type="error" title="无法读取迁移任务" description={errorMessage(query.error)} /></>
  const snapshot = query.data
  const terminal = ['COMPLETED', 'FAILED', 'CANCELLED'].includes(snapshot.run.status)
  const effectiveTarget = topology.data?.currentObservation && !topology.data.currentObservation.error ? topology.data.currentObservation.graph : topology.data?.target
  const requiredTargetNodes = effectiveTarget?.nodes.filter((node) => node.required && node.status !== 'SKIPPED') ?? []
  const currentSucceeded = requiredTargetNodes.filter((node) => node.status === 'SUCCEEDED').length
  const currentFailedOrMissing = requiredTargetNodes.filter((node) => ['FAILED', 'MISSING'].includes(node.status)).length
  const hasCurrentObservation = !!topology.data?.currentObservation && !topology.data.currentObservation.error
  const historicalFailureCurrentlyHealthy = snapshot.run.status === 'FAILED' && hasCurrentObservation && requiredTargetNodes.length > 0 && currentSucceeded === requiredTargetNodes.length
  const finishedSteps = snapshot.steps.filter((step) => ['SUCCEEDED', 'FAILED', 'SKIPPED'].includes(step.status)).length
  const canRetry = terminal
  const menuItems: NonNullable<MenuProps['items']> = []
  if (canRetry) menuItems.push(
    { key: 'retry', icon: <RedoOutlined />, label: '重新执行' },
    { key: 'edit', icon: <EditOutlined />, label: '编辑任务' },
  )
  if (terminal) menuItems.push(
    { type: 'divider' },
    { key: 'cleanup', icon: <DeleteOutlined />, label: '清理备份', danger: true },
    { key: 'delete', icon: <DeleteOutlined />, label: '删除任务', danger: true },
  )
  if (!terminal && snapshot.run.status !== 'ROLLING_BACK' && snapshot.run.status !== 'AWAITING_CUTOVER') menuItems.push(
    { key: 'cancel', icon: <StopOutlined />, label: '取消迁移', danger: true },
  )
  const onMenuClick: MenuProps['onClick'] = ({ key }) => {
    if (key === 'edit') { void editAndRetry(); return }
    if (key === 'retry') modal.confirm({ title: '重新执行此迁移？', content: '系统会基于同一计划创建一个新 Run，当前任务与证据保持不变。', okText: '重新执行', cancelText: '取消', onOk: retry })
    if (key === 'cleanup') modal.confirm({ title: '清理本次迁移备份？', content: 'Velero 备份及恢复对象将被删除；请仅在回滚窗口结束后执行。', okText: '确认清理', okButtonProps: { danger: true }, cancelText: '保留', onOk: cleanupArtifacts })
    if (key === 'delete') modal.confirm({ title: '删除这条迁移任务记录？', content: '仅从任务列表移除，不删除源端、目标端业务、备份或迁移证据。', okText: '删除任务', okButtonProps: { danger: true }, cancelText: '取消', onOk: removeRun })
    if (key === 'cancel') modal.confirm({ title: '确认取消当前迁移？', content: '源业务已停止时，系统会先进入回滚并恢复源业务。', okText: '确认取消', okButtonProps: { danger: true }, cancelText: '返回', onOk: cancel })
  }
  const actions = <Space wrap>
    {snapshot.run.status === 'COMPLETED' && snapshot.run.errorCode !== 'SOURCE_RESTORED' && <Popconfirm title="恢复源端业务？" description="系统将重新启动源端 Compose 服务或恢复 Kubernetes 副本；目标资源和迁移证据保留，DNS/LB 不会自动切回。" okText="恢复源端" cancelText="取消" onConfirm={restoreSource}><Button icon={<RollbackOutlined />} loading={busy}>恢复源端</Button></Popconfirm>}
    {snapshot.run.status === 'AWAITING_CUTOVER' && <Popconfirm title="确认外部流量已经切换？" description="确认后任务完成，系统不会再自动恢复源业务。" okText="确认完成" cancelText="返回" onConfirm={cutover}><Button type="primary" loading={busy}>确认切流</Button></Popconfirm>}
    {snapshot.run.status === 'AWAITING_CUTOVER' && <Popconfirm title="确认放弃切流并恢复源业务？" description="Worker 将恢复源端副本或重新启动 Compose 服务，目标资源保留用于诊断。" okText="恢复源业务" cancelText="返回" onConfirm={rollback}><Button icon={<RollbackOutlined />} loading={busy}>恢复源端</Button></Popconfirm>}
    <Button icon={<DownloadOutlined />} href={migrationReportURL(runID)}>下载完整报告</Button>
    {menuItems.length > 0 && <Dropdown menu={{ items: menuItems, onClick: onMenuClick }} placement="bottomRight" trigger={['click']}><Button icon={<MoreOutlined />} loading={busy} aria-label="更多任务操作" /></Dropdown>}
  </Space>
  return <>
    <PageHeader title={`迁移任务 #${snapshot.run.runNumber}`} description={`Run ID · ${snapshot.run.id}`} action={actions} />
    <RunAlert status={snapshot.run.status} errorCode={snapshot.run.errorCode} error={snapshot.run.errorMessage} historicalFailureCurrentlyHealthy={historicalFailureCurrentlyHealthy} currentSucceeded={currentSucceeded} currentTotal={requiredTargetNodes.length} observedAt={topology.data?.currentObservation?.checkedAt} />
    {actionError && <Alert className="page-alert" showIcon type="error" title="操作失败" description={actionError} />}
    <Card className="section-card run-evidence-summary" variant="outlined">
      <div className="run-evidence-metrics">
        <Statistic title="任务结果" value={historicalFailureCurrentlyHealthy ? '历史失败，当前正常' : snapshot.run.status === 'COMPLETED' && snapshot.run.errorCode === 'SOURCE_RESTORED' ? '完成，源端已恢复' : statusLabels[snapshot.run.status]} styles={{ content: { fontSize: 22 } }} />
        <Statistic title="步骤执行完成度" value={finishedSteps} suffix={`/ ${snapshot.steps.length}`} styles={{ content: { fontSize: 22 } }} />
        <Statistic title="目标当前正常资源" value={currentSucceeded} suffix={`/ ${requiredTargetNodes.length}`} styles={{ content: { fontSize: 22 } }} />
        <Statistic title="当前失败 / 缺失" value={currentFailedOrMissing} styles={{ content: { fontSize: 22 } }} />
        <Statistic title="已传输数据" value={formatBytes(snapshot.run.bytesTransferred ?? 0)} styles={{ content: { fontSize: 22 } }} />
      </div>
      <div className="run-progress-caption"><Text type="secondary">流程执行进度仅表示步骤已运行，不代表迁移成功；最终结论以任务结果和逐资源验证为准。</Text><Text>{snapshot.run.progress}%</Text></div>
      <Progress percent={snapshot.run.progress} status={snapshot.run.status === 'COMPLETED' ? 'success' : snapshot.run.status === 'FAILED' ? 'normal' : 'active'} strokeColor={snapshot.run.status === 'FAILED' ? '#8c9bab' : undefined} />
    </Card>
    <Card className="section-card run-evidence-tabs" variant="outlined">
      <Tabs activeKey={['topology', 'timeline', 'data', 'info'].includes(searchParams.get('tab') ?? '') ? searchParams.get('tab')! : terminal ? 'topology' : 'timeline'} onChange={(tab) => setSearchParams((current) => { const next = new URLSearchParams(current); next.set('tab', tab); return next })} destroyOnHidden={false} items={[
        { key: 'topology', label: '资源拓扑', children: <MigrationTopology evidence={topology.data} loading={topology.isLoading} refreshing={refreshingTopology} onRefresh={refreshTopology} /> },
        { key: 'timeline', label: '执行时序', children: <MigrationExecutionTimeline value={timeline.data} fallbackEvents={events} loading={timeline.isLoading} /> },
        { key: 'data', label: '数据迁移', children: <VolumeTransfersTable loading={transfers.isLoading} values={transfers.data ?? []} /> },
        { key: 'info', label: '任务信息', children: <RunInformation snapshot={snapshot} topology={topology.data} /> },
      ]} />
    </Card>
  </>
}

function RunAlert({ status, errorCode, error, historicalFailureCurrentlyHealthy, currentSucceeded, currentTotal, observedAt }: { status: MigrationRunStatus; errorCode?: string; error?: string; historicalFailureCurrentlyHealthy?: boolean; currentSucceeded?: number; currentTotal?: number; observedAt?: string }) {
  if (status === 'AWAITING_CUTOVER') return <Alert className="page-alert" showIcon type="warning" title="目标验证已通过，等待人工切流" description="系统不会自动修改 DNS、负载均衡或防火墙。完成外部切流后再确认。" />
  if (status === 'ROLLING_BACK') return <Alert className="page-alert" showIcon type="warning" title="正在恢复源业务" description="停机后取消或验证失败必须先完成源端恢复。" />
  if (status === 'FAILED' && historicalFailureCurrentlyHealthy) return <Alert className="page-alert" showIcon type="warning" title="历史执行失败，目标当前正常" description={`执行时验证未在窗口内通过；最近一次检查显示目标必需资源 ${currentSucceeded}/${currentTotal} 正常（${formatTime(observedAt)}）。历史失败证据保持不变，当前业务状态以资源拓扑和实际访问结果为准。`} />
  if (status === 'FAILED') return <Alert className="page-alert" showIcon type="error" title="迁移执行失败" description={error || '查看执行时序定位失败步骤；目标当前状态请以资源拓扑的最近检查结果为准。'} />
  if (status === 'COMPLETED' && errorCode === 'SOURCE_RESTORED') return <Alert className="page-alert" showIcon type="success" title="迁移已完成，源端已恢复" description="目标资源和本次迁移证据保持有效；源业务已重新启动，外部流量需按实际方案人工确认。" />
  if (status === 'CANCELLED') return <Alert className="page-alert" showIcon type="info" title="迁移已取消" description="源业务未停机，或已完成恢复。可以创建新的重试任务。" />
  if (status === 'COMPLETED') return <Alert className="page-alert" showIcon type="success" title="迁移已完成" />
  return <Alert className="page-alert" showIcon type="info" title={statusLabels[status]} description="页面刷新或服务重启不会丢失进度；事件由数据库游标继续读取。" />
}

function RunStatusTag({ status }: { status: MigrationRunStatus }) {
  const css = status === 'COMPLETED' ? 'status-success' : status === 'FAILED' ? 'status-error' : status === 'CANCELLED' ? 'status-attention' : 'status-running'
  return <Tag className={`status-tag ${css}`}>{statusLabels[status]}</Tag>
}
function VolumeTransfersTable({ loading, values }: { loading: boolean; values: VolumeTransfer[] }) {
  return <Table<VolumeTransfer> rowKey="id" pagination={false} size="small" loading={loading} dataSource={values} locale={{ emptyText: '当前任务没有卷数据，或尚未开始传输' }} columns={[
    { title: '引擎', dataIndex: 'engine', render: (value) => <Tag>{value}</Tag> },
    { title: '源卷', key: 'source', render: (_, value) => `${value.namespace}/${value.sourceVolume}` },
    { title: '目标 PVC', dataIndex: 'targetVolume' },
    { title: '数据量', key: 'bytes', render: (_, value) => `${formatBytes(value.transferredBytes)} / ${formatBytes(value.totalBytes)}` },
    { title: '吞吐', dataIndex: 'throughputBytesPerSecond', render: (value) => value ? `${formatBytes(value)}/s` : '—' },
    { title: '重试', dataIndex: 'retryCount' },
    { title: '校验', dataIndex: 'checksumStatus' },
    { title: '状态', dataIndex: 'status', render: (value) => <Tag color={value === 'COMPLETED' ? 'success' : value === 'FAILED' ? 'error' : 'processing'}>{value}</Tag> },
  ]} />
}
function RunInformation({ snapshot, topology }: { snapshot: MigrationRunSnapshot; topology?: TopologyEvidence }) {
  return <Descriptions bordered column={2} size="small" items={[
    { key: 'status', label: '状态', children: <RunStatusTag status={snapshot.run.status} /> },
    { key: 'plan', label: 'MigrationPlan', children: <Text copyable>{snapshot.run.migrationPlanId}</Text> },
    { key: 'created', label: '创建时间', children: formatTime(snapshot.run.createdAt) },
    { key: 'started', label: '开始时间', children: formatTime(snapshot.run.startedAt) },
    { key: 'completed', label: '完成时间', children: formatTime(snapshot.run.completedAt) },
    { key: 'bytes', label: '数据进度', children: `${formatBytes(snapshot.run.bytesTransferred ?? 0)} / ${formatBytes(snapshot.run.bytesTotal ?? 0)}` },
    { key: 'origin', label: '拓扑证据来源', children: topology?.snapshotOrigin === 'RECONSTRUCTED' ? '历史补建' : topology?.snapshotOrigin || '—' },
    { key: 'sourceCaptured', label: '源快照时间', children: formatTime(topology?.sourceCapturedAt) },
    { key: 'targetCaptured', label: '目标快照时间', children: formatTime(topology?.targetCapturedAt) },
    { key: 'observed', label: '当前状态检查', children: formatTime(topology?.currentObservation?.checkedAt) },
  ]} />
}
function formatTime(value?: string) { return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value)) : '—' }
function formatBytes(value: number) { if (!value) return '0 B'; const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1); return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}` }
function errorMessage(value: unknown) { return value instanceof Error ? value.message : '请求失败' }

const previewRunID = '0f589331-c56a-4578-9518-31b4bca26b71'
const previewSnapshot: MigrationRunSnapshot = {
  run: { id: previewRunID, migrationPlanId: 'ae3dac9f-7f13-4db1-a04e-69af509c2bdb', runNumber: 3, status: 'PRESYNC', progress: 24, bytesTotal: 274877906944, bytesTransferred: 68719476736, startedAt: '2026-09-04T01:12:00Z', createdAt: '2026-09-04T01:11:50Z', updatedAt: '2026-09-04T01:18:20Z' },
  steps: [
    previewStep('PREFLIGHT', 'SUCCEEDED', 100, 0), previewStep('PRESYNC', 'RUNNING', 68, 1), previewStep('QUIESCE', 'PENDING', 0, 2),
    previewStep('FINAL_BACKUP', 'PENDING', 0, 3), previewStep('TRANSFER', 'PENDING', 0, 4), previewStep('TRANSFORM', 'PENDING', 0, 5),
    previewStep('RESTORE', 'PENDING', 0, 6), previewStep('VALIDATION', 'PENDING', 0, 7), previewStep('AWAIT_CUTOVER', 'PENDING', 0, 8),
  ],
}
function previewStep(type: MigrationStep['type'], status: MigrationStep['status'], progress: number, offset: number): MigrationStep { return { id: `step-${offset}`, migrationRunId: previewRunID, type, attempt: 1, status, progress, idempotencyKey: `${type}:v1`, createdAt: `2026-09-04T01:12:0${offset}Z`, updatedAt: '2026-09-04T01:18:20Z' } }
const previewEvents: MigrationEvent[] = [
  { id: 81, migrationRunId: previewRunID, type: 'RUN_CREATED', severity: 'INFO', message: '迁移任务已进入持久化队列', createdAt: '2026-09-04T01:11:50Z' },
  { id: 82, migrationRunId: previewRunID, type: 'STEP_SUCCEEDED', severity: 'INFO', message: '执行期检查已通过', createdAt: '2026-09-04T01:12:08Z' },
  { id: 83, migrationRunId: previewRunID, type: 'STEP_STARTED', severity: 'INFO', message: 'PVC 数据在线预同步已开始', createdAt: '2026-09-04T01:12:10Z' },
]
const previewTransfers: VolumeTransfer[] = [{
  id: 'transfer-1', migrationRunId: previewRunID, engine: 'VELERO_FSB', namespace: 'business', sourceVolume: 'redis-0/data', targetVolume: 'data',
  totalBytes: 274877906944, transferredBytes: 68719476736, throughputBytesPerSecond: 536870912, retryCount: 0, checksumStatus: 'PENDING', status: 'RUNNING',
  createdAt: '2026-09-04T01:12:10Z', updatedAt: '2026-09-04T01:18:20Z',
}]
const previewTopology: TopologyEvidence = {
  migrationRunId: previewRunID, snapshotOrigin: 'NATIVE', sourceCapturedAt: '2026-09-04T01:11:50Z',
  source: { name: 'sida', type: 'KUBERNETES', namespace: 'business', nodes: [
    { id: 'src-deploy', side: 'SOURCE', apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'business', name: 'postgrest', required: true, status: 'DISCOVERED' },
    { id: 'src-svc', side: 'SOURCE', apiVersion: 'v1', kind: 'Service', namespace: 'business', name: 'postgrest', required: true, status: 'DISCOVERED' },
    { id: 'src-pvc', side: 'SOURCE', apiVersion: 'v1', kind: 'PersistentVolumeClaim', namespace: 'business', name: 'postgres-data', required: true, status: 'DISCOVERED' },
  ], edges: [{ id: 'e1', from: 'src-svc', to: 'src-deploy', relation: 'SELECTS', required: true }] },
  target: { name: 'mw', type: 'KUBERNETES', namespace: 'business-migrated', nodes: [
    { id: 'dst-deploy', side: 'TARGET', apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'business-migrated', name: 'postgrest', required: true, status: 'CREATED', mappingChanges: [{ type: 'IMAGE', sourceValue: 'docker.io/postgrest/postgrest', targetValue: 'harbor/sks/postgrest', changed: true, applied: true }] },
    { id: 'dst-svc', side: 'TARGET', apiVersion: 'v1', kind: 'Service', namespace: 'business-migrated', name: 'postgrest', required: true, status: 'SUCCEEDED' },
    { id: 'dst-pvc', side: 'TARGET', apiVersion: 'v1', kind: 'PersistentVolumeClaim', namespace: 'business-migrated', name: 'postgres-data', required: true, status: 'SUCCEEDED', mappingChanges: [{ type: 'STORAGE_CLASS', sourceValue: 'nfs-csi-velero-lab', targetValue: 'smtx-elf-csi-driver', changed: true, applied: true }] },
  ], edges: [{ id: 'e2', from: 'dst-svc', to: 'dst-deploy', relation: 'SELECTS', required: true }] },
  mappings: [
    { id: 'm1', sourceNodeId: 'src-deploy', targetNodeId: 'dst-deploy', relation: 'MIGRATES_TO' },
    { id: 'm2', sourceNodeId: 'src-svc', targetNodeId: 'dst-svc', relation: 'MIGRATES_TO' },
    { id: 'm3', sourceNodeId: 'src-pvc', targetNodeId: 'dst-pvc', relation: 'MIGRATES_TO' },
  ],
}
const previewTimeline: StepTimeline = {
  migrationRunId: previewRunID, generatedAt: '2026-09-04T01:18:20Z', events: previewEvents, diagnosis: { state: 'RUNNING', title: '迁移正常运行', reason: 'Worker 心跳有效，正在等待 Velero Backup 完成。', lastSuccessfulStep: 'PREFLIGHT' },
  steps: previewSnapshot.steps.map((step) => ({ step, attempts: step.startedAt ? [{ stepId: step.id, attempt: 1, status: step.status === 'SUCCEEDED' ? 'SUCCEEDED' : 'RUNNING', startedAt: step.startedAt, heartbeatAt: '2026-09-04T01:18:20Z' }] : [], events: previewEvents.filter((event) => event.detail?.stepId === step.id) })),
}
