import { useEffect, useMemo, useState, type Key } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import {
  CheckCircleOutlined, CloseCircleOutlined, CloudServerOutlined, ContainerOutlined,
  ExclamationCircleOutlined, RightOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import Drawer from '../components/DetailDrawer'
import {
  Alert, Button, Card, Checkbox, Descriptions, Form, Input, Segmented, Select, Space, Spin, Steps,
  Switch, Table, Tag, Typography,
} from 'antd'
import {
  createAssessment, createMigrationPlan, discoverAllKubernetesApplications, discoverComposeApplications, discoverSelectedKubernetesApplication,
  listApplications, listEnvironmentNamespaces, listEnvironments, listMappingProfiles, previewKubernetesApplication,
  preflightMigrationPlan, startMigrationRun, type Assessment, type Environment, type MappingProfile,
  type KubernetesInventory, type MigrationPlan, type MigrationPlanInput, type PreflightCheck, type PreflightResult, type ResourceSummary, type SourceApplication,
} from '../api/client'
import PageHeader from '../components/PageHeader'

const { Paragraph, Text, Title } = Typography
const steps = ['源环境', '选择应用', '迁移评估', '目标映射', '迁移检查', '确认'].map((title) => ({ title }))

export default function NewMigrationPage({ preview = false }: { preview?: boolean }) {
  const navigate = useNavigate()
  const { state } = useLocation()
  const draft = state?.draft as MigrationPlan | undefined
  const [current, setCurrent] = useState(0)
  const [sourceType, setSourceType] = useState<'KUBERNETES' | 'COMPOSE' | null>(state?.sourceType ?? null)
  const [sourceId, setSourceId] = useState(draft?.sourceEnvironmentId ?? '')
  const [applicationId, setApplicationId] = useState(draft?.sourceApplicationId ?? '')
  const [targetId, setTargetId] = useState(draft?.targetEnvironmentId ?? '')
  const [assessment, setAssessment] = useState<Assessment>()
  const [mappingId, setMappingId] = useState(draft?.mappingProfileId ?? '')
  const [planName, setPlanName] = useState(draft ? `${draft.name}（重试）` : '')
  const [volumeMode, setVolumeMode] = useState<MigrationPlanInput['strategy']['volumeMode']>(draft?.strategy.volumeMode ?? 'FS_BACKUP')
  const [preSync, setPreSync] = useState(draft?.strategy.preSyncEnabled ?? true)
  const [httpChecks, setHTTPChecks] = useState(draft?.validationPolicy.httpChecks?.join('\n') ?? '')
  const [plan, setPlan] = useState<MigrationPlan>()
  const [preflight, setPreflight] = useState<PreflightResult>()
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const environments = useQuery({ queryKey: ['environments'], queryFn: listEnvironments, enabled: !preview, initialData: preview ? previewEnvironments : undefined })
  const applications = useQuery({ queryKey: ['applications', sourceId], queryFn: () => listApplications(sourceId), enabled: !preview && !!sourceId, initialData: preview && sourceId ? previewApplications.filter((item) => item.environmentId === sourceId) : undefined })
  const mappings = useQuery({ queryKey: ['mapping-profiles', targetId], queryFn: () => listMappingProfiles(targetId), enabled: !preview && !!targetId, initialData: preview && targetId ? previewMappings.filter((item) => item.targetEnvironmentId === targetId) : undefined })

  const sources = useMemo(() => (environments.data ?? []).filter((item) => item.role === 'SOURCE' && (!sourceType || item.kind === (sourceType === 'COMPOSE' ? 'DOCKER_COMPOSE' : 'KUBERNETES'))), [environments.data, sourceType])
  const targets = useMemo(() => (environments.data ?? []).filter((item) => item.role === 'TARGET' && item.kind === 'KUBERNETES'), [environments.data])
  const selectedSource = (environments.data ?? []).find((item) => item.id === sourceId)
  const selectedApplication = (applications.data ?? []).find((item) => item.id === applicationId)
  const selectedTarget = targets.find((item) => item.id === targetId)
  const selectedMapping = (mappings.data ?? []).find((item) => item.id === mappingId)

  useEffect(() => {
    if (import.meta.env.MODE !== 'test') window.scrollTo({ top: 0, behavior: 'auto' })
  }, [current])

  function chooseType(value: 'KUBERNETES' | 'COMPOSE') {
    setSourceType(value); setApplicationId(''); setAssessment(undefined); setPlan(undefined); setPreflight(undefined)
    const expectedKind = value === 'COMPOSE' ? 'DOCKER_COMPOSE' : 'KUBERNETES'
    setSourceId((environments.data ?? []).find((item) => item.role === 'SOURCE' && item.kind === expectedKind)?.id ?? '')
    setVolumeMode(value === 'COMPOSE' ? 'COMPOSE_KOPIA' : 'FS_BACKUP')
  }

  async function next() {
    setError('')
    if (current === 2 && !assessment) {
      setBusy(true)
      try {
        const value = preview ? previewAssessment : await createAssessment(applicationId, targetId)
        setAssessment(value)
        return
      } catch (reason) { setError(errorMessage(reason)); return } finally { setBusy(false) }
    }
    if (current === 3) {
      setBusy(true)
      try {
        const input: MigrationPlanInput = {
          name: planName.trim(), sourceEnvironmentId: sourceId, targetEnvironmentId: targetId,
          sourceApplicationId: applicationId, assessmentId: assessment!.id, mappingProfileId: mappingId,
          strategy: { resourceMode: 'TRANSFORM', overwriteExistingResources: false, preserveNodePort: false, ...draft?.strategy, volumeMode, preSyncEnabled: preSync },
          validationPolicy: { requireWorkloadsReady: true, requirePVCsBound: true, timeoutSeconds: 300, tcpChecks: [], ...draft?.validationPolicy, httpChecks: splitLines(httpChecks) },
        }
        const created = preview ? { ...input, id: 'preview-plan', status: 'DRAFT' as const, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() } : await createMigrationPlan(input)
        const checked = preview ? previewPreflight : await preflightMigrationPlan(created.id)
        setPlan(created); setPreflight(checked); setCurrent(4)
        return
      } catch (reason) { setError(errorMessage(reason)); return } finally { setBusy(false) }
    }
    if (current < 5) setCurrent(current + 1)
  }

  async function confirmRun() {
    setBusy(true); setError('')
    try {
      if (preview) { navigate('/__preview/run'); return }
      const value = await startMigrationRun(plan!.id)
      navigate(`/migrations/${value.run.id}`)
    } catch (reason) { setError(errorMessage(reason)) } finally { setBusy(false) }
  }

  function previous() { if (current > 0 && current < 4) { setError(''); setCurrent(current - 1) } }
  const nextDisabled = current === 0 ? !sourceType || !sourceId : current === 1 ? !applicationId : current === 2 ? !targetId || (!!assessment && assessment.blockerCount > 0) : current === 3 ? !mappingId || !planName.trim() : current === 4 ? !preflight?.ready : !confirmed

  return <>
    <PageHeader title="创建迁移" description="发现、评估并迁移应用到 SmartX SKS 工作负载集群。" />
    <Card className="wizard-shell" variant="outlined">
      <Steps current={current} items={steps} responsive={false} />
      <div className="wizard-content">
        {current === 0 && <SourceStep sourceType={sourceType} sourceId={sourceId} sources={sources} onType={chooseType} onSource={setSourceId} />}
        {current === 1 && <ApplicationStep preview={preview} source={selectedSource} applications={applications.data ?? []} value={applicationId} onChange={setApplicationId} loading={applications.isPending} onRefresh={() => applications.refetch()} />}
        {current === 2 && <AssessmentStep targets={targets} targetId={targetId} onTarget={(value) => { setTargetId(value); setAssessment(undefined); setMappingId('') }} assessment={assessment} />}
        {current === 3 && <MappingStep planName={planName} onPlanName={setPlanName} mappings={mappings.data ?? []} mappingId={mappingId} onMapping={setMappingId} sourceType={sourceType!} volumeMode={volumeMode} onVolumeMode={setVolumeMode} preSync={preSync} onPreSync={setPreSync} httpChecks={httpChecks} onHTTPChecks={setHTTPChecks} />}
        {current === 4 && <PreflightStep value={preflight} />}
        {current === 5 && <ConfirmationStep plan={plan} source={selectedSource} application={selectedApplication} target={selectedTarget} mapping={selectedMapping} preflight={preflight} checked={confirmed} onCheck={setConfirmed} />}
        {error && <Alert className="wizard-error" showIcon type="error" title="无法继续" description={error} />}
      </div>
      <div className="wizard-footer">
        <Button onClick={() => current === 0 ? navigate('/migrations') : previous()} disabled={current >= 4}>{current === 0 ? '取消' : '上一步'}</Button>
        <Button type="primary" loading={busy} disabled={nextDisabled} onClick={() => current === 5 ? confirmRun() : next()}>{current === 2 && !assessment ? '执行评估' : current === 3 ? '保存并执行检查' : current === 5 ? '确认并启动' : '下一步'}</Button>
      </div>
    </Card>
  </>
}

function SourceStep({ sourceType, sourceId, sources, onType, onSource }: { sourceType: 'KUBERNETES' | 'COMPOSE' | null; sourceId: string; sources: Environment[]; onType: (value: 'KUBERNETES' | 'COMPOSE') => void; onSource: (value: string) => void }) {
  return <><Title level={2}>选择迁移来源</Title><Paragraph>两类来源使用相同的评估、映射、检查、迁移和验证流程。</Paragraph><div className="migration-type-grid">
    <button type="button" className={`migration-type-card ${sourceType === 'KUBERNETES' ? 'selected' : ''}`} onClick={() => onType('KUBERNETES')}><CloudServerOutlined /><span><strong>Kubernetes → SKS</strong><small>迁移 Namespace、资源与 PVC 数据</small></span><RightOutlined /></button>
    <button type="button" className={`migration-type-card ${sourceType === 'COMPOSE' ? 'selected' : ''}`} onClick={() => onType('COMPOSE')}><ContainerOutlined /><span><strong>Docker Compose → SKS</strong><small>转换服务、配置与持久化卷</small></span><RightOutlined /></button>
  </div>{sourceType && <Form.Item className="wizard-select-field" label="已导入的源环境" required><Select value={sourceId || undefined} onChange={onSource} placeholder="选择源环境" options={sources.map((item) => ({ value: item.id, label: `${item.name} · ${item.status}` }))} /></Form.Item>}<Alert type="info" showIcon title="迁移中心只连接工作负载集群，不操作 Cluster API 管控集群，也不会自动修改 DNS 或负载均衡。" /></>
}

function ApplicationStep({ preview, source, applications, value, onChange, loading, onRefresh }: { preview: boolean; source?: Environment; applications: SourceApplication[]; value: string; onChange: (value: string) => void; loading: boolean; onRefresh: () => Promise<unknown> }) {
  const [mode, setMode] = useState<'AUTO' | 'MANUAL'>('AUTO')
  const [namespace, setNamespace] = useState<string>()
  const [inventory, setInventory] = useState<KubernetesInventory>()
  const [selectedKeys, setSelectedKeys] = useState<Key[]>([])
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')
  const namespaces = useQuery({
    queryKey: ['environment-namespaces', source?.id], queryFn: () => listEnvironmentNamespaces(source?.id ?? ''),
    enabled: !preview && source?.kind === 'KUBERNETES' && mode === 'MANUAL', initialData: preview ? ['business', 'default'] : undefined,
  })
  const selected = applications.find((item) => item.id === value)

  async function discoverAll() {
    if (!source) return
    setBusy(true); setFailure('')
    try {
      const values = preview ? applications : source.kind === 'KUBERNETES' ? await discoverAllKubernetesApplications(source.id) : await discoverComposeApplications(source.id)
      await onRefresh()
      if (values[0]) onChange(values[0].id)
    } catch (reason) { setFailure(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function loadInventory() {
    if (!source || !namespace) return
    setBusy(true); setFailure('')
    try {
      const value = preview ? previewApplications[0].inventory : await previewKubernetesApplication(source.id, namespace)
      setInventory(value); setSelectedKeys([]); setName(`${namespace}-selection`)
    } catch (reason) { setFailure(errorMessage(reason)) } finally { setBusy(false) }
  }
  async function saveSelection() {
    if (!source || !namespace || !inventory || selectedKeys.length === 0) return
    setBusy(true); setFailure('')
    try {
      const resources = inventory.resources.filter((item) => selectedKeys.includes(resourceKey(item)))
      const created = preview ? previewApplications[0] : await discoverSelectedKubernetesApplication(source.id, namespace, name, resources)
      await onRefresh(); onChange(created.id)
    } catch (reason) { setFailure(errorMessage(reason)) } finally { setBusy(false) }
  }

  const manual = source?.kind === 'KUBERNETES' && mode === 'MANUAL'
  return <><Title level={2}>选择源应用</Title><Paragraph>{source?.kind === 'KUBERNETES' ? '可自动发现有业务资源的 Namespace，也可按 Namespace 手动选择任意资源组合。' : '从主机的 docker compose ls 自动发现项目，也可在源环境页手动登记 Compose 文件。'}</Paragraph>
    {source?.kind === 'KUBERNETES' && <Segmented className="application-selection-mode" value={mode} onChange={(next) => { setMode(next as 'AUTO' | 'MANUAL'); setInventory(undefined); setFailure('') }} options={[{ value: 'AUTO', label: '自动发现应用' }, { value: 'MANUAL', label: '按 Namespace 手选资源' }]} />}
    {!manual ? <>
      <Space className="application-discovery-actions"><Button onClick={discoverAll} loading={busy}>{source?.kind === 'KUBERNETES' ? '扫描全部 Namespace' : '从 Compose 主机发现应用'}</Button><Text type="secondary">当前已登记 {applications.length} 个应用</Text></Space>
      <Form.Item className="wizard-select-field" label="源应用" required><Select value={value || undefined} onChange={onChange} loading={loading} placeholder="选择已发现应用" options={applications.map((item) => ({ value: item.id, label: `${item.name}${item.namespace ? ` · ${item.namespace}` : item.inventory.compose?.workingDir ? ` · ${item.inventory.compose.workingDir}` : ''}` }))} /></Form.Item>
    </> : <>
      <div className="manual-inventory-controls"><Form.Item label="Namespace" required><Select showSearch value={namespace} onChange={(next) => { setNamespace(next); setInventory(undefined); setSelectedKeys([]) }} loading={namespaces.isPending} options={(namespaces.data ?? []).map((item) => ({ value: item, label: item }))} placeholder="选择 Namespace" /></Form.Item><Button onClick={loadInventory} loading={busy} disabled={!namespace}>读取资源</Button></div>
      {inventory && <><div className="manual-selection-heading"><span><Text strong>选择迁移资源</Text><Text type="secondary"> 已选 {selectedKeys.length} / {inventory.resources.length}</Text></span><Button type="link" onClick={() => setSelectedKeys(inventory.resources.map(resourceKey))}>全选</Button></div>
        <Table<ResourceSummary> className="manual-resource-table" size="small" rowKey={resourceKey} pagination={{ pageSize: 8, hideOnSinglePage: true }} dataSource={inventory.resources} rowSelection={{ selectedRowKeys: selectedKeys, onChange: setSelectedKeys }} columns={[{ title: '类型', dataIndex: 'kind', width: 180, render: (kind) => <Tag>{kind}</Tag> }, { title: '名称', dataIndex: 'name' }, { title: '关联信息', key: 'detail', render: (_, item) => item.images?.join('、') || item.ingressClassName || item.serviceType || item.dataKeys?.join('、') || item.secretKeys?.join('、') || '—' }]} />
        <div className="manual-inventory-save"><Input value={name} onChange={(event) => setName(event.target.value)} placeholder="为这组资源命名" maxLength={128} /><Button type="primary" onClick={saveSelection} loading={busy} disabled={!name.trim() || selectedKeys.length === 0}>保存为迁移应用</Button></div>
      </>}
    </>}
    {!loading && !manual && applications.length === 0 && <Alert showIcon type="warning" title="该环境还没有应用 Inventory" description="点击上方发现按钮，或返回源环境手动登记应用。" />}
    {failure && <Alert className="wizard-error" showIcon type="error" title="应用发现失败" description={failure} />}
    {selected && <Descriptions size="small" bordered column={3} items={[{ key: 'type', label: '源类型', children: selected.sourceType }, { key: 'resources', label: '资源对象', children: selected.inventory.resources.length }, { key: 'pvc', label: 'PVC', children: selected.inventory.pvcs.length }]} />}</>
}

function AssessmentStep({ targets, targetId, onTarget, assessment }: { targets: Environment[]; targetId: string; onTarget: (value: string) => void; assessment?: Assessment }) {
  const [filter, setFilter] = useState<'ALL' | 'BLOCKER' | 'WARNING'>()
  const issues = assessment?.issues.filter((item) => filter === 'ALL' || item.severity === filter) ?? []
  return <><Title level={2}>迁移评估</Title><Paragraph>选择目标 SKS 工作负载集群，并执行能力快照兼容性评估。点击得分或问题数可查看规则细则。</Paragraph><Form.Item className="wizard-select-field" label="目标 SKS" required><Select value={targetId || undefined} onChange={onTarget} placeholder="选择目标工作负载集群" options={targets.map((item) => ({ value: item.id, label: `${item.name} · ${item.capabilities.kubernetesVersion ?? '版本未知'}` }))} /></Form.Item>{assessment && <div className="wizard-assessment-summary"><button type="button" onClick={() => setFilter('ALL')}><Card size="small"><Text type="secondary">兼容性得分 · 查看细则</Text><strong>{assessment.score}</strong></Card></button><button type="button" onClick={() => setFilter('BLOCKER')}><Card size="small"><Text type="secondary">BLOCKER · 查看原因</Text><strong className={assessment.blockerCount ? 'text-error' : 'text-success'}>{assessment.blockerCount}</strong></Card></button><button type="button" onClick={() => setFilter('WARNING')}><Card size="small"><Text type="secondary">WARNING · 查看原因</Text><strong>{assessment.warningCount}</strong></Card></button></div>}{assessment?.blockerCount ? <Alert showIcon type="error" title="BLOCKER 未清零，不能创建迁移计划" description="点击 BLOCKER 卡片查看受影响资源、判定规则和修复建议。" /> : assessment && <Alert showIcon type="success" title="评估门禁通过，可以继续配置目标映射。" />}
    <Drawer title={filter === 'BLOCKER' ? 'BLOCKER 原因与处理建议' : filter === 'WARNING' ? 'WARNING 细则' : '兼容性评分细则'} size="large" open={Boolean(filter)} onClose={() => setFilter(undefined)} destroyOnHidden>
      {issues.length === 0 ? <Alert showIcon type="success" title={`没有 ${filter === 'ALL' ? '兼容性问题' : filter} 项`} /> : <div className="assessment-issue-list">{issues.map((issue) => <Card key={issue.id} size="small" title={<Space><Tag color={issue.severity === 'BLOCKER' ? 'error' : issue.severity === 'WARNING' ? 'warning' : 'blue'}>{issue.severity}</Tag>{issue.title}</Space>} extra={<Tag>{issue.category}</Tag>}><Descriptions size="small" column={1} items={[{ key: 'rule', label: '规则 ID', children: issue.ruleId }, { key: 'resource', label: '受影响资源', children: `${issue.resourceKind} ${issue.resourceNamespace ? `${issue.resourceNamespace}/` : ''}${issue.resourceName}` }, { key: 'reason', label: '判定原因', children: issue.description }, { key: 'remediation', label: '处理建议', children: issue.remediation || '请调整源资源或目标映射后重新评估。' }]} /></Card>)}</div>}
    </Drawer></>
}

function resourceKey(item: ResourceSummary) { return `${item.kind}\u0000${item.namespace ?? ''}\u0000${item.name}` }

function MappingStep({ planName, onPlanName, mappings, mappingId, onMapping, sourceType, volumeMode, onVolumeMode, preSync, onPreSync, httpChecks, onHTTPChecks }: { planName: string; onPlanName: (value: string) => void; mappings: MappingProfile[]; mappingId: string; onMapping: (value: string) => void; sourceType: 'KUBERNETES' | 'COMPOSE'; volumeMode: MigrationPlanInput['strategy']['volumeMode']; onVolumeMode: (value: MigrationPlanInput['strategy']['volumeMode']) => void; preSync: boolean; onPreSync: (value: boolean) => void; httpChecks: string; onHTTPChecks: (value: string) => void }) {
  const volumeOptions = sourceType === 'COMPOSE' ? [{ value: 'COMPOSE_KOPIA', label: 'Kopia 文件级迁移' }] : [{ value: 'FS_BACKUP', label: 'Velero FSB / Kopia' }, { value: 'CSI_DATA_MOVER', label: 'CSI Snapshot Data Mover' }, { value: 'NONE', label: '不迁移卷数据' }]
  return <><Title level={2}>目标映射与策略</Title><Paragraph>选择已经过冲突校验的目标映射，并确定卷数据和验证策略。</Paragraph><div className="wizard-form-grid"><Form.Item label="计划名称" required><Input value={planName} maxLength={128} onChange={(event) => onPlanName(event.target.value)} placeholder="例如：生产订单服务迁移" /></Form.Item><Form.Item label="目标映射" required><Select value={mappingId || undefined} onChange={onMapping} options={mappings.map((item) => ({ value: item.id, label: item.name }))} placeholder="选择 MappingProfile" /></Form.Item><Form.Item label="卷迁移方式"><Select value={volumeMode} onChange={onVolumeMode} options={volumeOptions} /></Form.Item><Form.Item label="在线预同步"><Switch checked={preSync} onChange={onPreSync} checkedChildren="启用" unCheckedChildren="关闭" /></Form.Item></div><Form.Item label="HTTP 验证地址（每行一个，可选）"><Input.TextArea value={httpChecks} onChange={(event) => onHTTPChecks(event.target.value)} rows={3} placeholder="https://app.example.local/healthz" /></Form.Item><Alert showIcon type="warning" title="最终同步会停止源业务；目标验证失败或人工切流前取消时，系统会恢复源端副本或 Compose 服务。" /></>
}

function PreflightStep({ value }: { value?: PreflightResult }) {
  if (!value) return <div className="wizard-loading"><Spin /><Text>正在执行迁移检查…</Text></div>
  return <><Title level={2}>迁移检查</Title><Alert showIcon type={value.ready ? 'success' : 'error'} title={value.ready ? '所有阻塞门禁已通过' : `发现 ${value.blockerCount} 个 BLOCKER`} description={value.ready ? `仍有 ${value.warningCount} 个执行期检查项。` : '必须修复所有 BLOCKER 后重新创建或检查计划。'} /><div className="preflight-checks">{value.checks.map((item) => <PreflightCheckRow key={item.id} value={item} />)}</div></>
}

function PreflightCheckRow({ value }: { value: PreflightCheck }) {
  const icon = value.status === 'PASSED' ? <CheckCircleOutlined /> : value.status === 'WARNING' ? <ExclamationCircleOutlined /> : <CloseCircleOutlined />
  return <div className={`preflight-check preflight-${value.status.toLowerCase()}`}>{icon}<span><strong>{value.title}</strong><small>{value.message}</small>{value.remediation && <small className="preflight-remediation">建议：{value.remediation}</small>}</span><Tag>{value.category}</Tag></div>
}

function ConfirmationStep({ plan, source, application, target, mapping, preflight, checked, onCheck }: { plan?: MigrationPlan; source?: Environment; application?: SourceApplication; target?: Environment; mapping?: MappingProfile; preflight?: PreflightResult; checked: boolean; onCheck: (value: boolean) => void }) {
  return <><Title level={2}>确认迁移计划</Title><Alert showIcon type="success" title="计划已保存并通过 Preflight" description="确认后创建持久化任务并交给 Worker 异步执行；页面刷新不会丢失进度。" /><Descriptions bordered column={2} items={[{ key: 'name', label: '计划名称', children: plan?.name }, { key: 'status', label: '状态', children: <Tag color="success">READY</Tag> }, { key: 'source', label: '源环境', children: source?.name }, { key: 'application', label: '源应用', children: application?.name }, { key: 'target', label: '目标 SKS', children: target?.name }, { key: 'mapping', label: '目标映射', children: mapping?.name }, { key: 'mode', label: '卷迁移', children: plan?.strategy.volumeMode }, { key: 'checks', label: '检查结果', children: `${preflight?.checks.length ?? 0} 项 / ${preflight?.warningCount ?? 0} WARNING` }]} /><Checkbox className="cutover-confirmation" checked={checked} onChange={(event) => onCheck(event.target.checked)}>我已确认：系统不会自动修改 DNS、负载均衡或防火墙；业务切流需要人工确认。</Checkbox></>
}

function splitLines(value: string) { return value.split('\n').map((item) => item.trim()).filter(Boolean) }
function errorMessage(value: unknown) { return value instanceof Error ? value.message : '请求失败' }

const previewEnvironments: Environment[] = [
  { id: 'source-k8s', name: '生产 Kubernetes', role: 'SOURCE', kind: 'KUBERNETES', status: 'CONNECTED', capabilities: { kubernetesVersion: 'v1.29.8' }, createdAt: '2026-09-03T01:00:00Z', updatedAt: '2026-09-03T01:00:00Z' },
  { id: 'source-compose', name: '边缘 Compose 主机', role: 'SOURCE', kind: 'DOCKER_COMPOSE', status: 'CONNECTED', capabilities: {}, createdAt: '2026-09-03T01:00:00Z', updatedAt: '2026-09-03T01:00:00Z' },
  { id: 'target-sks', name: 'SKS-Production', role: 'TARGET', kind: 'KUBERNETES', status: 'CONNECTED', capabilities: { kubernetesVersion: 'v1.32.6', csiDrivers: ['smtx-elf-csi-driver'] }, createdAt: '2026-09-03T01:00:00Z', updatedAt: '2026-09-03T01:00:00Z' },
]
const previewApplications: SourceApplication[] = [{ id: 'application-k8s', environmentId: 'source-k8s', name: 'business', sourceType: 'KUBERNETES', namespace: 'business', inventory: { resources: [{ kind: 'Deployment', name: 'api' }], workloads: [], services: [], ingresses: [], configMaps: [], secrets: [], serviceAccounts: [], roles: [], roleBindings: [], crds: [], customResources: [], pvcs: [{ name: 'data', storageClassName: 'legacy-block' }], images: [], dependencies: [], warnings: [], counts: { Deployment: 1 } }, createdAt: '2026-09-03T01:00:00Z', updatedAt: '2026-09-03T01:00:00Z' }]
const previewMappings: MappingProfile[] = [{ id: 'mapping-default', name: '生产默认映射', targetEnvironmentId: 'target-sks', storageMappings: [], namespaceMappings: [], ingressMappings: [], registryMappings: [], nfsMappings: [], nodeLabelMappings: [], createdAt: '2026-09-03T01:00:00Z', updatedAt: '2026-09-03T01:00:00Z' }]
const previewAssessment: Assessment = { id: 'assessment-ready', applicationId: 'application-k8s', score: 92, blockerCount: 0, warningCount: 2, infoCount: 1, status: 'COMPLETED', issues: [], createdAt: '2026-09-03T01:00:00Z', completedAt: '2026-09-03T01:01:00Z' }
const previewPreflight: PreflightResult = { migrationPlanId: 'preview-plan', ready: true, blockerCount: 0, warningCount: 1, checks: [
  { id: 'assessment.blockers', category: 'ASSESSMENT', status: 'PASSED', title: 'BLOCKER 门禁', message: '没有未解决的阻塞项。' },
  { id: 'source.connection', category: 'SOURCE', status: 'PASSED', title: '源环境连接', message: '连接状态正常。' },
  { id: 'target.smartx-csi', category: 'STORAGE', status: 'PASSED', title: 'SmartX 块存储 CSI', message: '已发现 smtx-elf-csi-driver。' },
  { id: 'strategy.fsb-runtime', category: 'STORAGE', status: 'WARNING', title: 'FSB 节点访问', message: '执行前将在线验证 hostPath、MountPropagation 和 kubelet root 路径。' },
] }
