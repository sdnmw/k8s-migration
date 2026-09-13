import { Alert, Descriptions, Empty, Space, Table, Tabs, Tag, Typography } from 'antd'
import Drawer from './DetailDrawer'
import type { Environment, SourceApplication } from '../api/client'
import AssessmentPanel from './AssessmentPanel'

const { Text } = Typography

export default function KubernetesInventoryDrawer({ application, targets, preview = false, onClose }: { application?: SourceApplication; targets: Environment[]; preview?: boolean; onClose: () => void }) {
  const inventory = application?.inventory
  return <Drawer title={`${application?.namespace ?? ''} · Namespace Inventory`} size={720} open={Boolean(application)} onClose={onClose} destroyOnHidden>
    {inventory && <>
      {inventory.warnings.length > 0 && <Alert className="inventory-warning" showIcon type="warning" title={`${inventory.warnings.length} 项资源未完整读取`} description={inventory.warnings.map((item) => `${item.resource || item.code}: ${item.message}`).join('；')} />}
      <Descriptions bordered size="small" column={4} items={[
        { key: 'resources', label: '资源', children: inventory.resources.length },
        { key: 'workloads', label: '工作负载', children: inventory.workloads.length },
        { key: 'pvcs', label: 'PVC', children: inventory.pvcs.length },
        { key: 'dependencies', label: '依赖边', children: inventory.dependencies.length },
      ]} />
      <Tabs items={[
        { key: 'resources', label: `资源 (${inventory.resources.length})`, children: <ResourceTable resources={inventory.resources} /> },
        { key: 'dependencies', label: `依赖 (${inventory.dependencies.length})`, children: <Table size="small" pagination={{ pageSize: 8 }} rowKey={(item) => `${ref(item.from)}-${item.type}-${ref(item.to)}`} dataSource={inventory.dependencies} columns={[
          { title: '来源', dataIndex: 'from', render: ref }, { title: '关系', dataIndex: 'type', width: 158, render: (value) => <Tag>{dependencyLabel(value)}</Tag> }, { title: '目标', dataIndex: 'to', render: ref },
        ]} /> },
        { key: 'storage', label: `PVC (${inventory.pvcs.length})`, children: <Table size="small" pagination={false} rowKey="name" dataSource={inventory.pvcs} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="无 PVC" /> }} columns={[
          { title: 'PVC', dataIndex: 'name' }, { title: '容量', dataIndex: 'capacityBytes', render: formatBytes }, { title: 'StorageClass', dataIndex: 'storageClassName', render: (value) => value || '—' }, { title: '访问模式', dataIndex: 'accessModes', render: (values) => values?.join('、') || '—' },
        ]} /> },
        { key: 'images', label: `镜像 (${inventory.images.length})`, children: <Space direction="vertical" size={8}>{inventory.images.map((image) => <Text code key={image.reference}>{image.reference}</Text>)}</Space> },
        { key: 'assessment', label: '迁移评估', children: application && <AssessmentPanel application={application} targets={targets} preview={preview} /> },
      ]} />
    </>}
  </Drawer>
}

function ResourceTable({ resources }: { resources: SourceApplication['inventory']['resources'] }) {
  return <Table size="small" rowKey={(item) => `${item.apiVersion}/${item.kind}/${item.namespace}/${item.name}`} dataSource={resources} pagination={{ pageSize: 8 }} columns={[
    { title: '类型', dataIndex: 'kind', width: 160 }, { title: '名称', dataIndex: 'name' },
    { title: '镜像 / 数据键', key: 'detail', render: (_, item) => item.images?.join('、') || keyCount(item.secretKeys, 'Secret') || keyCount(item.dataKeys, 'ConfigMap') || '—' },
  ]} />
}

function ref(value: { kind: string; namespace?: string; name: string }) { return `${value.kind}/${value.namespace ? `${value.namespace}/` : ''}${value.name}` }
function keyCount(values: string[] | undefined, label: string) { return values?.length ? `${label} ${values.length} 个键` : '' }
function dependencyLabel(value: string) { return ({ SELECTS: '选择工作负载', ROUTES_TO: '路由到', MOUNTS: '挂载', READS_ENV_FROM: '读取环境', READS_ENV_KEY: '读取键', USES_STORAGE_CLASS: '使用存储类', USES_SERVICE_ACCOUNT: '使用服务账号', USES_IMAGE_PULL_SECRET: '拉取镜像', BINDS_ROLE: '绑定角色', GRANTS_TO: '授权给', OWNED_BY: '归属于', SCALES: '弹性伸缩' } as Record<string, string>)[value] || value }
function formatBytes(value?: number) { if (!value) return '—'; const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let amount = value; let unit = 0; while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit++ } return `${Number.isInteger(amount) ? amount : amount.toFixed(1)} ${units[unit]}` }
