import { DeleteOutlined, EditOutlined, EyeOutlined, MoreOutlined, RedoOutlined } from '@ant-design/icons'
import { Button, Dropdown, Progress, Table, Tag, Tooltip, type MenuProps, type TableProps } from 'antd'
import { useNavigate } from 'react-router-dom'
import type { MigrationRow, MigrationStatusLabel } from '../migrations/presentation'

const statusClass: Record<MigrationStatusLabel, string> = {
  '迁移中': 'status-running',
  '等待切流': 'status-attention',
  '成功': 'status-success',
  '成功有告警': 'status-attention',
  '源端已恢复': 'status-attention',
  '需要处理': 'status-error',
}

type MigrationTableProps = {
  compact?: boolean
  rows: MigrationRow[]
  loading?: boolean
  busyRunId?: string
  onRetry?: (row: MigrationRow) => void
  onEdit?: (row: MigrationRow) => void
  onDelete?: (row: MigrationRow) => void
}

export default function MigrationTable({ compact = false, rows, loading = false, busyRunId, onRetry, onEdit, onDelete }: MigrationTableProps) {
  const navigate = useNavigate()
  const textCell = (value: string) => <Tooltip title={value} placement="topLeft"><span className="migration-table-cell-text">{value}</span></Tooltip>
  const columns: TableProps<MigrationRow>['columns'] = [
    { title: '名称', dataIndex: 'name', width: compact ? 210 : 250, ellipsis: true, render: (value, row) => <Tooltip title={value} placement="topLeft"><Button type="link" className="table-link" onClick={() => navigate(`/migrations/${row.key}`)}>{value}</Button></Tooltip> },
    { title: '类型', dataIndex: 'type', width: compact ? 112 : 128, ellipsis: true, render: textCell },
    { title: '源环境', dataIndex: 'source', width: compact ? 116 : 130, ellipsis: true, render: textCell },
    { title: '目标', dataIndex: 'target', width: compact ? 90 : 100, ellipsis: true, render: textCell },
    ...(!compact ? [{ title: '应用 / Namespace', dataIndex: 'application', width: 180, ellipsis: true, render: textCell }] : []),
    { title: '状态', dataIndex: 'status', width: compact ? 88 : 104, render: (value: MigrationStatusLabel) => <Tag className={`status-tag ${statusClass[value]}`}>{value}</Tag> },
    { title: '进度', dataIndex: 'progress', width: compact ? 126 : 150, render: (value: number) => <Progress percent={value} size="small" showInfo={value < 100} /> },
    { title: '数据量', dataIndex: 'data', width: compact ? 82 : 96, ellipsis: true, render: textCell },
    { title: '创建时间', dataIndex: 'createdAt', width: compact ? 106 : 128, ellipsis: true, render: textCell },
    ...(!compact ? [{ title: '耗时', dataIndex: 'duration', width: 100, ellipsis: true, render: textCell }] : []),
    {
      title: '操作', key: 'action', fixed: 'right', width: compact ? 70 : 88,
      render: (_, row) => {
        const terminal = ['COMPLETED', 'FAILED', 'CANCELLED'].includes(row.rawStatus)
        const items: MenuProps['items'] = [
          { key: 'view', icon: <EyeOutlined />, label: '查看详情' },
          { type: 'divider' },
          { key: 'retry', icon: <RedoOutlined />, label: '重新执行', disabled: !terminal || !onRetry },
          { key: 'edit', icon: <EditOutlined />, label: '编辑任务', disabled: !terminal || !onEdit },
          { type: 'divider' },
          { key: 'delete', icon: <DeleteOutlined />, label: '删除任务', danger: true, disabled: !terminal || !onDelete },
        ]
        const onMenuClick: MenuProps['onClick'] = ({ key, domEvent }) => {
          domEvent.stopPropagation()
          if (key === 'view') navigate(`/migrations/${row.key}`)
          if (key === 'retry') onRetry?.(row)
          if (key === 'edit') onEdit?.(row)
          if (key === 'delete') onDelete?.(row)
        }
        return <Dropdown menu={{ items, onClick: onMenuClick }} placement="bottomRight" trigger={['click']}>
          <Tooltip title="更多操作"><Button type="text" icon={<MoreOutlined />} loading={busyRunId === row.key} aria-label={`${row.name} 更多操作`} onClick={(event) => event.stopPropagation()} /></Tooltip>
        </Dropdown>
      },
    },
  ]
  return <Table<MigrationRow> className="migration-table" tableLayout="fixed" columns={columns} dataSource={rows} loading={loading} pagination={false} locale={{ emptyText: '暂无迁移任务' }} scroll={{ x: compact ? 1100 : 1420 }} />
}
