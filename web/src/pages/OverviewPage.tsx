import { ArrowRightOutlined, CheckCircleOutlined, ClockCircleOutlined, ExclamationCircleOutlined } from '@ant-design/icons'
import { Button, Card, Col, Flex, Row, Space, Statistic, Tag, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { listEnvironments, listMigrationRuns, type Environment, type MigrationRunSummary } from '../api/client'
import MigrationTable from '../components/MigrationTable'
import { isActiveStatus, toMigrationRow } from '../migrations/presentation'
import PageHeader from '../components/PageHeader'

const { Text, Title } = Typography

export default function OverviewPage({ preview = false }: { preview?: boolean }) {
  const navigate = useNavigate()
  const runs = useQuery({ queryKey: ['migration-runs'], queryFn: listMigrationRuns, enabled: !preview, initialData: preview ? previewRuns : undefined, refetchInterval: preview ? false : 10_000 })
  const environments = useQuery({ queryKey: ['environments'], queryFn: listEnvironments, enabled: !preview, initialData: preview ? previewEnvironments : undefined })
  const runValues = runs.data ?? []
  const environmentValues = environments.data ?? []
  const failed = runValues.filter((value) => value.status === 'FAILED' || value.status === 'CANCELLED').length
  const awaiting = runValues.filter((value) => value.status === 'AWAITING_CUTOVER').length
  const summaries = [
    { label: '迁移任务', value: runValues.length, hint: '全部执行记录' },
    { label: '迁移成功', value: runValues.filter((value) => value.status === 'COMPLETED').length, hint: '已确认切流' },
    { label: '迁移中', value: runValues.filter((value) => isActiveStatus(value.status)).length, hint: `其中 ${awaiting} 个等待人工切流` },
    { label: '需要处理', value: failed, hint: failed ? '失败或已取消任务' : '当前没有异常任务' },
  ]
  return (
    <>
      <PageHeader title="概览" description="查看迁移任务进展与源端、目标端环境状态。" />
      <Row gutter={16} className="summary-grid">
        {summaries.map((item) => (
          <Col span={6} key={item.label}>
            <Card className="summary-card" variant="outlined">
              <Statistic title={item.label} value={item.value} />
              <Text type="secondary">{item.hint}</Text>
            </Card>
          </Col>
        ))}
      </Row>
      <Card
        className="section-card" variant="outlined" title="最近迁移任务"
        extra={<Button type="link" onClick={() => navigate('/migrations')}>查看全部 <ArrowRightOutlined /></Button>}
      >
        <MigrationTable compact rows={runValues.slice(0, 5).map((value) => toMigrationRow(value))} loading={runs.isPending} />
      </Card>
      <Card className="section-card" variant="outlined" title="环境状态">
        <div className="environment-status-grid">
          {environmentValues.map((environment) => <EnvironmentStatus key={environment.id} value={environment} />)}
          {!environments.isPending && environmentValues.length === 0 && <div className="overview-empty">尚未导入源环境或目标 SKS 集群</div>}
        </div>
      </Card>
    </>
  )
}

function EnvironmentStatus({ value }: { value: Environment }) {
  const connected = value.status === 'CONNECTED'
  const pending = value.status === 'PENDING'
  const Icon = connected ? CheckCircleOutlined : pending ? ClockCircleOutlined : ExclamationCircleOutlined
  const semanticClass = connected ? 'semantic-success' : pending ? 'semantic-warning' : 'semantic-error'
  const tagClass = connected ? 'status-success' : pending ? 'status-attention' : 'status-error'
  const tag = connected ? (value.role === 'TARGET' ? '可迁移' : '连接正常') : pending ? '待连接' : '需要处理'
  const kind = value.kind === 'DOCKER_COMPOSE' ? 'Docker Compose' : (value.capabilities.kubernetesVersion ? `Kubernetes ${value.capabilities.kubernetesVersion}` : 'Kubernetes')
  const role = value.role === 'TARGET' ? '目标工作负载集群' : '源环境'
  return <Flex align="center" justify="space-between" className="environment-status-item">
    <Space><Icon className={semanticClass} /><div><Title level={3}>{value.name}</Title><Text type="secondary">{role} · {kind}</Text></div></Space>
    <Tag className={`status-tag ${tagClass}`}>{tag}</Tag>
  </Flex>
}

const previewRuns: MigrationRunSummary[] = [
  { id: '10000000-0000-4000-8000-000000000001', migrationPlanId: '20000000-0000-4000-8000-000000000001', runNumber: 1, planName: '生产订单服务迁移', sourceType: 'KUBERNETES', sourceEnvironmentName: 'sida', targetEnvironmentName: 'mw', applicationName: 'business', applicationNamespace: 'business', status: 'TRANSFER', progress: 68, bytesTotal: 459561500672, bytesTransferred: 312501820456, startedAt: '2026-09-05T06:32:00Z', createdAt: '2026-09-05T06:32:00Z', updatedAt: '2026-09-05T07:50:00Z' },
  { id: '10000000-0000-4000-8000-000000000002', migrationPlanId: '20000000-0000-4000-8000-000000000002', runNumber: 1, planName: '仓储 Redis 迁移', sourceType: 'COMPOSE', sourceEnvironmentName: 'warehouse-docker-01', targetEnvironmentName: 'mw', applicationName: 'warehouse', status: 'AWAITING_CUTOVER', progress: 96, bytesTotal: 88046829568, bytesTransferred: 84470218752, startedAt: '2026-09-05T02:08:00Z', createdAt: '2026-09-05T02:08:00Z', updatedAt: '2026-09-05T05:34:00Z' },
]

const previewEnvironments: Environment[] = [
  { id: 'source-preview', name: 'sida', role: 'SOURCE', kind: 'KUBERNETES', status: 'CONNECTED', capabilities: { kubernetesVersion: 'v1.32.6', nodeCount: 3 }, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-05T00:00:00Z' },
  { id: 'target-preview', name: 'mw', role: 'TARGET', kind: 'KUBERNETES', status: 'CONNECTED', capabilities: { kubernetesVersion: 'v1.32.6', nodeCount: 3 }, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-05T00:00:00Z' },
]
