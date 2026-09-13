import { PlusOutlined, SearchOutlined } from '@ant-design/icons'
import { App as AntApplication, Button, Card, Input, Segmented, Space } from 'antd'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import MigrationTable from '../components/MigrationTable'
import PageHeader from '../components/PageHeader'
import { deleteMigrationRun, getMigrationPlan, listEnvironments, listMigrationRuns, retryMigrationRun } from '../api/client'
import { isActiveStatus, toMigrationRow, type MigrationRow } from '../migrations/presentation'

export default function MigrationsPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { message, modal } = AntApplication.useApp()
  const [filter, setFilter] = useState('全部')
  const [query, setQuery] = useState('')
  const [busyRunId, setBusyRunId] = useState('')
  const runs = useQuery({ queryKey: ['migration-runs'], queryFn: listMigrationRuns, refetchInterval: 10_000 })
  const rows = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return (runs.data ?? []).map((value) => toMigrationRow(value)).filter((migration) => {
      const matchesFilter = filter === '全部'
        || (filter === '进行中' && isActiveStatus(migration.rawStatus))
        || (filter === '成功' && migration.status === '成功')
        || (filter === '失败' && migration.rawStatus === 'FAILED')
        || (filter === '需要处理' && ['等待切流', '源端已恢复', '需要处理'].includes(migration.status))
      const matchesQuery = !normalizedQuery
        || [migration.name, migration.source, migration.target, migration.application]
          .some((value) => value.toLowerCase().includes(normalizedQuery))
      return matchesFilter && matchesQuery
    })
  }, [filter, query, runs.data])

  function retry(row: MigrationRow) {
    modal.confirm({
      title: `重新执行“${row.name}”？`, content: '将基于同一迁移计划创建一个新 Run，当前任务及证据保持不变。', okText: '重新执行', cancelText: '取消',
      onOk: async () => {
        setBusyRunId(row.key)
        try { const value = await retryMigrationRun(row.key); navigate(`/migrations/${value.run.id}`) }
        catch (error) { message.error(errorMessage(error)) } finally { setBusyRunId('') }
      },
    })
  }

  async function edit(row: MigrationRow) {
    const summary = runs.data?.find((item) => item.id === row.key)
    if (!summary) return
    setBusyRunId(row.key)
    try {
      const [draft, environments] = await Promise.all([getMigrationPlan(summary.migrationPlanId), listEnvironments()])
      const source = environments.find((item) => item.id === draft.sourceEnvironmentId)
      if (!source) throw new Error('原源环境已不存在，请重新创建迁移并选择有效源环境。')
      navigate('/migrations/new', { state: { draft, sourceType: source.kind === 'DOCKER_COMPOSE' ? 'COMPOSE' : 'KUBERNETES' } })
    } catch (error) { message.error(errorMessage(error)) } finally { setBusyRunId('') }
  }

  function remove(row: MigrationRow) {
    modal.confirm({
      title: `删除“${row.name}”？`, content: '仅从任务列表移除记录，不删除源端、目标端业务、备份或迁移证据。', okText: '删除任务', okButtonProps: { danger: true }, cancelText: '取消',
      onOk: async () => {
        setBusyRunId(row.key)
        try { await deleteMigrationRun(row.key); await queryClient.invalidateQueries({ queryKey: ['migration-runs'] }); message.success('任务已从列表移除') }
        catch (error) { message.error(errorMessage(error)) } finally { setBusyRunId('') }
      },
    })
  }
  return (
    <>
      <PageHeader
        title="迁移任务" description="统一管理 Kubernetes 与 Docker Compose 到 SmartX SKS 的迁移。"
        action={<Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/migrations/new')}>创建迁移</Button>}
      />
      <Card className="section-card table-card" variant="outlined">
        <Space className="table-toolbar" wrap>
          <Segmented options={['全部', '进行中', '成功', '失败', '需要处理']} value={filter} onChange={(value) => setFilter(String(value))} />
          <Input allowClear prefix={<SearchOutlined />} placeholder="搜索任务或源环境" value={query} onChange={(event) => setQuery(event.target.value)} />
        </Space>
        <MigrationTable rows={rows} loading={runs.isPending} busyRunId={busyRunId} onRetry={retry} onEdit={edit} onDelete={remove} />
      </Card>
    </>
  )
}

function errorMessage(value: unknown) { return value instanceof Error ? value.message : '操作失败，请稍后重试' }
