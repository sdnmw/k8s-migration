import {
  CheckCircleOutlined, CloudServerOutlined, DatabaseOutlined, HddOutlined,
  KeyOutlined, LinkOutlined, PlusOutlined, SafetyCertificateOutlined,
} from '@ant-design/icons'
import { Alert, Button, Card, Descriptions, Empty, Space, Table, Tag, Typography } from 'antd'
import PageHeader from '../components/PageHeader'

const { Text, Title } = Typography

const sectionConfig = {
  'source-environments': { title: '源环境', description: '导入 Kubernetes kubeconfig 或注册 Docker Compose 主机。', action: '添加源环境', icon: <CloudServerOutlined /> },
  'sks-environments': { title: 'SmartX SKS', description: '逐个导入目标工作负载集群 kubeconfig；不接入 CAPI 管控集群。', action: '添加 SKS 集群', icon: <CloudServerOutlined /> },
  'storage-mapping': { title: '资源映射', description: '管理 StorageClass、外部 NFS、Namespace、IngressClass 与 NodeLabel 映射。', action: '创建资源映射', icon: <HddOutlined /> },
  'registry-mapping': { title: '镜像仓库', description: '检查镜像架构与认证，并配置到目标 Harbor 的 digest 映射。', action: '添加仓库', icon: <LinkOutlined /> },
  'transform-rules': { title: '资源转换规则', description: '查看资源规范化、Registry 改写和目标兼容性转换规则。', action: '新建规则', icon: <SafetyCertificateOutlined /> },
  'object-storage': { title: '对象存储', description: '管理目标 SKS 内的单实例 MinIO 与 Velero BackupStorageLocation。', action: '部署 MinIO', icon: <DatabaseOutlined /> },
  'credentials': { title: '凭证', description: '管理 kubeconfig、SSH、Registry 与 S3 凭证；凭证内容不可回读。', action: '添加凭证', icon: <KeyOutlined /> },
} as const

export type SectionKind = keyof typeof sectionConfig | 'migration-detail'

export default function SectionPage({ kind }: { kind: SectionKind }) {
  if (kind === 'migration-detail') return <MigrationDetailPlaceholder />
  const section = sectionConfig[kind]
  const isSKS = kind === 'sks-environments'
  return (
    <>
      <PageHeader title={section.title} description={section.description} action={<Button type="primary" icon={<PlusOutlined />}>{section.action}</Button>} />
      {isSKS && <Alert className="page-alert" showIcon type="info" title="这里只登记 SKS 工作负载集群。Cluster API 管控集群不在系统操作范围内。" />}
      <Card className="section-card empty-state-card" variant="outlined">
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<><Title level={3}>尚未配置{section.title}</Title><Text type="secondary">完成配置后，状态和能力信息将在这里显示。</Text></>}>
          <Button icon={section.icon}>{section.action}</Button>
        </Empty>
      </Card>
    </>
  )
}

function MigrationDetailPlaceholder() {
  const rows = [
    { key: '1', volume: 'data-postgresql-0', total: '256 GiB', transferred: '173 GiB', throughput: '86 MiB/s', status: '迁移中' },
    { key: '2', volume: 'uploads', total: '172 GiB', transferred: '172 GiB', throughput: '112 MiB/s', status: '已完成' },
  ]
  return (
    <>
      <PageHeader title="生产订单服务迁移" description="Kubernetes · manufacturing → SKS-Production" action={<Button danger>取消迁移</Button>} />
      <Alert className="page-alert" showIcon type="warning" title="源业务仍在运行；当前为在线预同步阶段。最终停机前系统会再次要求确认。" />
      <div className="detail-grid">
        <Card className="section-card" title="迁移进度" variant="outlined">
          <ol className="migration-timeline">
            <li className="done"><CheckCircleOutlined /><span><strong>环境检查</strong><small>已完成</small></span></li>
            <li className="done"><CheckCircleOutlined /><span><strong>创建备份</strong><small>已完成</small></span></li>
            <li className="active"><span className="timeline-dot" /><span><strong>PVC 数据预同步</strong><small>428 GiB / 68%</small></span></li>
            {['停止源业务', '最终同步', '目标恢复', '应用验证', '等待人工切流'].map((item) => <li key={item}><span className="timeline-dot" /><span><strong>{item}</strong><small>等待执行</small></span></li>)}
          </ol>
        </Card>
        <Card className="section-card" title="任务信息" variant="outlined">
          <Descriptions column={1} size="small" items={[
            { key: 'source', label: '源环境', children: '生产 K8s' },
            { key: 'target', label: '目标环境', children: 'SKS-Production' },
            { key: 'strategy', label: '数据策略', children: 'Velero FSB / Kopia' },
            { key: 'cutover', label: '切流方式', children: '人工确认' },
          ]} />
        </Card>
      </div>
      <Card className="section-card" title="卷传输" variant="outlined">
        <Table dataSource={rows} pagination={false} columns={[
          { title: 'PVC', dataIndex: 'volume' }, { title: '总量', dataIndex: 'total' },
          { title: '已传输', dataIndex: 'transferred' }, { title: '速度', dataIndex: 'throughput' },
          { title: '状态', dataIndex: 'status', render: (value) => <Tag className={`status-tag ${value === '已完成' ? 'status-success' : 'status-running'}`}>{value}</Tag> },
        ]} />
        <Space className="technical-log-action"><Button type="link">查看技术日志</Button></Space>
      </Card>
    </>
  )
}
