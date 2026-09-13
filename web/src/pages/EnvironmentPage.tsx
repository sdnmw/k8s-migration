import { useMemo, useState, type ReactNode } from 'react'
import {
  ApartmentOutlined, CheckCircleOutlined, CloudServerOutlined, DeleteOutlined, DisconnectOutlined,
  ExclamationCircleOutlined, FileTextOutlined, PlusOutlined, ReloadOutlined,
  RadarChartOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, App as AntApplication, Button, Card, Descriptions, Form, Input, Popconfirm, Segmented, Select, Space, Table, Tag, Tooltip, Typography, Upload,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import Modal from '../components/EditorModal'
import Drawer from '../components/DetailDrawer'
import type { UploadProps } from 'antd'
import {
  APIError, createEnvironment, deleteEnvironment, discoverComposeApplications, discoverKubernetesApplication, listEnvironmentNamespaces, listEnvironments, refreshEnvironmentCapabilities,
  testEnvironmentConnection, type ClusterCapabilities, type ConnectionTest, type Environment, type EnvironmentRole, type SourceApplication,
} from '../api/client'
import PageHeader from '../components/PageHeader'
import ComposeAnalyzeModal from '../components/ComposeAnalyzeModal'
import KubernetesInventoryDrawer from '../components/KubernetesInventoryDrawer'

const maxKubeconfigBytes = 1024 * 1024
const { Text } = Typography

type FormValues = {
  name: string; credential?: string; endpoint?: string; username?: string; privateKey?: string
  password?: string; privateKeyPassword?: string; hostKeyFingerprint?: string; sshAuthMethod?: 'PASSWORD' | 'PRIVATE_KEY'
}

export default function EnvironmentPage({ role, preview = false }: { role: EnvironmentRole; preview?: boolean }) {
  const { message } = AntApplication.useApp()
  const [form] = Form.useForm<FormValues>()
  const [open, setOpen] = useState(false)
  const [composeOpen, setComposeOpen] = useState(false)
  const [sourceKind, setSourceKind] = useState<'KUBERNETES' | 'DOCKER_COMPOSE'>('KUBERNETES')
  const [inventoryEnvironment, setInventoryEnvironment] = useState<Environment>()
  const [selectedNamespace, setSelectedNamespace] = useState<string>()
  const [inventoryResult, setInventoryResult] = useState<SourceApplication>()
  const [testingID, setTestingID] = useState<string>()
  const [connectionResult, setConnectionResult] = useState<{ name: string; result: ConnectionTest }>()
  const [capabilityResult, setCapabilityResult] = useState<{ name: string; capabilities: ClusterCapabilities }>()
  const [discoveringID, setDiscoveringID] = useState<string>()
  const queryClient = useQueryClient()
  const sshAuthMethod = Form.useWatch('sshAuthMethod', form) ?? 'PASSWORD'
  const isTarget = role === 'TARGET'
  const environments = useQuery({
    queryKey: ['environments'], queryFn: listEnvironments, enabled: !preview,
    initialData: preview ? previewEnvironments : undefined,
  })
  const rows = useMemo(() => (environments.data ?? []).filter((item) => item.role === role), [environments.data, role])
  const namespaces = useQuery({
    queryKey: ['environment-namespaces', inventoryEnvironment?.id],
    queryFn: () => listEnvironmentNamespaces(inventoryEnvironment?.id ?? ''),
    enabled: Boolean(inventoryEnvironment) && !preview,
    initialData: preview ? ['business', 'default'] : undefined,
  })

  const createMutation = useMutation({
    mutationFn: (values: FormValues) => sourceKind === 'DOCKER_COMPOSE' && role === 'SOURCE'
      ? createEnvironment({
          name: values.name, role: 'SOURCE', kind: 'DOCKER_COMPOSE', endpoint: values.endpoint ?? '',
          ssh: { username: values.username ?? '', password: values.password, privateKey: values.privateKey, privateKeyPassword: values.privateKeyPassword, hostKeyFingerprint: values.hostKeyFingerprint ?? '' },
        })
      : createEnvironment({ name: values.name, role, kind: 'KUBERNETES', credential: values.credential ?? '' }),
    onSuccess: async () => {
      form.resetFields()
      setOpen(false)
      await queryClient.invalidateQueries({ queryKey: ['environments'] })
      message.success('环境已安全导入，请执行连接测试')
    },
  })
  const testMutation = useMutation({
    mutationFn: testEnvironmentConnection,
    onMutate: (id) => setTestingID(id),
    onSuccess: async (result, id) => {
      await queryClient.invalidateQueries({ queryKey: ['environments'] })
      setConnectionResult({ name: rows.find((item) => item.id === id)?.name ?? 'Kubernetes 集群', result })
      if (result.success) message.success('连接测试通过')
      else message.error(result.checks.find((item) => item.status === 'FAILED')?.message ?? '连接测试失败')
    },
    onError: (error) => message.error(errorMessage(error)),
    onSettled: () => setTestingID(undefined),
  })
  const deleteMutation = useMutation({
    mutationFn: deleteEnvironment,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['environments'] })
      message.success('环境及其专用凭证已删除')
    },
    onError: (error) => message.error(errorMessage(error)),
  })
  const discoveryMutation = useMutation({
    mutationFn: refreshEnvironmentCapabilities,
    onMutate: (id) => setDiscoveringID(id),
    onSuccess: async (capabilities, id) => {
      setCapabilityResult({ name: rows.find((item) => item.id === id)?.name ?? 'Kubernetes 集群', capabilities })
      await queryClient.invalidateQueries({ queryKey: ['environments'] })
      message.success('集群能力快照已刷新')
    },
    onError: (error) => message.error(errorMessage(error)),
    onSettled: () => setDiscoveringID(undefined),
  })
  const inventoryMutation = useMutation({
    mutationFn: () => preview ? Promise.resolve(previewApplication) : discoverKubernetesApplication(inventoryEnvironment?.id ?? '', selectedNamespace ?? ''),
    onSuccess: (value) => {
      setInventoryEnvironment(undefined); setSelectedNamespace(undefined); setInventoryResult(value)
      message.success(`已发现 ${value.inventory.resources.length} 个资源和 ${value.inventory.dependencies.length} 条依赖`)
    },
    onError: (error) => message.error(errorMessage(error)),
  })
  const composeDiscoveryMutation = useMutation({
    mutationFn: (environmentID: string) => discoverComposeApplications(environmentID),
    onMutate: (id) => setDiscoveringID(id),
    onSuccess: async (values) => {
      await queryClient.invalidateQueries({ queryKey: ['applications'] })
      if (values.length) message.success(`已发现并登记 ${values.length} 个 Compose 应用`)
      else message.warning('当前 SSH 用户的 Docker 环境中没有 Compose 项目。请确认连接的主机、用户和 Docker Context。')
    },
    onError: (error) => message.error(errorMessage(error)),
    onSettled: () => setDiscoveringID(undefined),
  })

  const columns: ColumnsType<Environment> = [
    {
      title: '名称', dataIndex: 'name', width: 190,
      render: (value: string, record) => <Space><CloudServerOutlined className="environment-kind-icon" /><span><Button type="link" className="table-link" onClick={() => setCapabilityResult({ name: record.name, capabilities: record.capabilities })}>{value}</Button><small className="table-secondary">{record.kind === 'KUBERNETES' ? 'Kubernetes' : 'Docker Compose'}</small></span></Space>,
    },
    { title: '连接地址', dataIndex: 'endpoint', ellipsis: true, render: (value?: string) => <Text copyable={Boolean(value)}>{value || '—'}</Text> },
    {
      title: '版本 / 规模', key: 'capabilities', width: 180,
      render: (_, record) => record.kind === 'DOCKER_COMPOSE' && record.capabilities.runtime
        ? <span><Text>Docker {record.capabilities.runtime.dockerVersion ?? '—'}</Text><small className="table-secondary">Compose {record.capabilities.runtime.composeVersion ?? '—'}</small></span>
        : record.capabilities.kubernetesVersion
        ? <span><Text>{record.capabilities.kubernetesVersion}</Text><small className="table-secondary">{record.capabilities.nodeCount ?? 0} 节点 · {record.capabilities.namespaceCount ?? 0} 命名空间</small></span>
        : <Text type="secondary">等待探测</Text>,
    },
    { title: '状态', dataIndex: 'status', width: 120, render: (value: Environment['status']) => <EnvironmentStatusTag status={value} /> },
    {
      title: '操作', key: 'actions', width: role === 'SOURCE' ? 326 : 248,
      render: (_, record) => (
        <Space size={4}>
          <Button type="link" icon={<ReloadOutlined />} loading={testingID === record.id} onClick={() => testMutation.mutate(record.id)}>测试连接</Button>
          {role === 'SOURCE' && <Button type="link" icon={<ApartmentOutlined />} loading={record.kind === 'DOCKER_COMPOSE' && discoveringID === record.id} onClick={() => record.kind === 'KUBERNETES' ? setInventoryEnvironment(record) : composeDiscoveryMutation.mutate(record.id)}>发现应用</Button>}
          <Button type="link" icon={<RadarChartOutlined />} loading={record.kind === 'KUBERNETES' && discoveringID === record.id} onClick={() => discoveryMutation.mutate(record.id)}>能力发现</Button>
          <Popconfirm title="删除环境？" description="环境专用连接凭证也会永久删除。" okText="删除" cancelText="取消" onConfirm={() => deleteMutation.mutate(record.id)}>
            <Tooltip title="删除"><Button type="text" danger aria-label={`删除 ${record.name}`} icon={<DeleteOutlined />} /></Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  const uploadProps: UploadProps = {
    accept: '.yaml,.yml,.conf', showUploadList: false,
    beforeUpload: async (file) => {
      if (file.size > maxKubeconfigBytes) {
        message.error('kubeconfig 不得超过 1 MiB')
        return Upload.LIST_IGNORE
      }
      form.setFieldValue('credential', await file.text())
      message.success(`已读取 ${file.name}`)
      return Upload.LIST_IGNORE
    },
  }

  const privateKeyUploadProps: UploadProps = {
    accept: '.pem,.key', showUploadList: false,
    beforeUpload: async (file) => {
      if (file.size > maxKubeconfigBytes) {
        message.error('SSH 私钥不得超过 1 MiB')
        return Upload.LIST_IGNORE
      }
      form.setFieldValue('privateKey', await file.text())
      message.success(`已读取 ${file.name}`)
      return Upload.LIST_IGNORE
    },
  }

  return (
    <>
      <PageHeader
        title={isTarget ? 'SmartX SKS' : '源环境'}
        description={isTarget ? '逐个导入目标工作负载集群 kubeconfig；不接入 CAPI 管控集群。' : '导入源 Kubernetes 集群，或通过固定主机指纹与密码/私钥登记 Docker Compose 主机。'}
        action={<Space>{!isTarget && <Button onClick={() => setComposeOpen(true)}>注册 Compose 应用</Button>}<Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>{isTarget ? '添加 SKS 集群' : '添加源环境'}</Button></Space>}
      />
      {isTarget && <Alert className="page-alert" showIcon type="info" title="这里只登记 SKS 工作负载集群。Cluster API 管控集群不在系统操作范围内。" />}
      {environments.isError && <Alert className="page-alert" showIcon type="error" title="无法读取环境" description={errorMessage(environments.error)} />}
      <Card className="section-card environment-table-card" variant="outlined">
        <Table<Environment>
          rowKey="id" columns={columns} dataSource={rows} loading={environments.isPending}
          locale={{ emptyText: '尚未导入集群' }} pagination={{ pageSize: 10, hideOnSinglePage: true }}
        />
      </Card>
      <Modal
        title={isTarget ? '导入 SKS 工作负载集群' : '添加源环境'} open={open}
        width={sourceKind === 'DOCKER_COMPOSE' && !isTarget ? 720 : undefined}
        style={sourceKind === 'DOCKER_COMPOSE' && !isTarget ? { top: 24 } : undefined}
        okText="安全导入" cancelText="取消" confirmLoading={createMutation.isPending}
        onOk={() => form.submit()} onCancel={() => { setOpen(false); form.resetFields(); createMutation.reset() }} destroyOnHidden
      >
        <Alert className="environment-modal-alert" showIcon type="info" title={`${sourceKind === 'DOCKER_COMPOSE' && !isTarget ? 'SSH 认证凭证' : 'kubeconfig'} 将使用主密钥加密，之后无法从界面回读。`} />
        <Form<FormValues> form={form} layout="vertical" preserve={false} onFinish={(values) => createMutation.mutate(values)}>
          {!isTarget && <Form.Item label="源类型"><Segmented block value={sourceKind} options={[{ label: 'Kubernetes', value: 'KUBERNETES' }, { label: 'Docker Compose', value: 'DOCKER_COMPOSE' }]} onChange={(value) => { setSourceKind(value as typeof sourceKind); form.resetFields() }} /></Form.Item>}
          <Form.Item name="name" label="环境名称" rules={[{ required: true, whitespace: true, message: '请输入环境名称' }, { max: 128 }]}>
            <Input placeholder={isTarget ? '例如：SKS-Production' : '例如：生产 Kubernetes'} autoComplete="off" />
          </Form.Item>
          {sourceKind === 'KUBERNETES' || isTarget ? <>
            <Form.Item label="kubeconfig" required><Upload {...uploadProps}><Button icon={<FileTextOutlined />}>选择本地文件</Button></Upload></Form.Item>
            <Form.Item name="credential" rules={[{ required: true, message: '请选择文件或粘贴 kubeconfig' }]}><Input.TextArea className="kubeconfig-input" rows={9} placeholder="也可以在此粘贴 kubeconfig 内容" autoComplete="off" spellCheck={false} /></Form.Item>
          </> : <>
            <div className="ssh-field-row">
              <Form.Item name="endpoint" label="SSH 地址" rules={[{ required: true, message: '请输入 ssh://host[:port]' }, { pattern: /^ssh:\/\/[^/]+$/, message: '格式必须为 ssh://host[:port]' }]}><Input placeholder="ssh://10.20.30.50:22" autoComplete="off" /></Form.Item>
              <Form.Item name="username" label="SSH 用户" rules={[{ required: true, whitespace: true }]}><Input placeholder="migration" autoComplete="username" /></Form.Item>
            </div>
            <div className="ssh-field-row ssh-field-row-wide">
              <Form.Item name="hostKeyFingerprint" label="主机指纹（可选）" extra="留空时自动读取并保存，后续连接会核对主机是否变化。" rules={[{ pattern: /^SHA256:/, message: '填写时必须以 SHA256: 开头' }]}><Input placeholder="自动获取，无需手动填写" autoComplete="off" /></Form.Item>
              <Form.Item name="sshAuthMethod" label="认证方式" initialValue="PASSWORD"><Segmented block options={[{ label: '密码', value: 'PASSWORD' }, { label: 'SSH 私钥', value: 'PRIVATE_KEY' }]} /></Form.Item>
            </div>
            {sshAuthMethod === 'PASSWORD' ? <Form.Item name="password" label="SSH 密码" rules={[{ required: true, message: '请输入 SSH 密码' }, { max: 4096 }]}><Input.Password autoComplete="new-password" /></Form.Item> : <>
              <Form.Item name="privateKeyPassword" label="私钥口令（可选）"><Input.Password autoComplete="new-password" /></Form.Item>
              <Form.Item label="SSH 私钥" required><Upload {...privateKeyUploadProps}><Button icon={<FileTextOutlined />}>选择私钥文件</Button></Upload></Form.Item>
              <Form.Item name="privateKey" rules={[{ required: true, message: '请选择文件或粘贴私钥' }]}><Input.TextArea className="kubeconfig-input" rows={4} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" autoComplete="off" spellCheck={false} /></Form.Item>
            </>}
          </>}
          {createMutation.isError && <Alert showIcon type="error" title="导入失败" description={errorMessage(createMutation.error)} />}
        </Form>
      </Modal>
      {!isTarget && <ComposeAnalyzeModal open={composeOpen} environments={rows.filter((item) => item.kind === 'DOCKER_COMPOSE')} onClose={() => setComposeOpen(false)} />}
      <Modal
        title={`${inventoryEnvironment?.name ?? ''} · 发现 Namespace 应用`} open={Boolean(inventoryEnvironment)} okText="开始发现" cancelText="取消"
        okButtonProps={{ disabled: !selectedNamespace }} confirmLoading={inventoryMutation.isPending}
        onOk={() => inventoryMutation.mutate()} onCancel={() => { setInventoryEnvironment(undefined); setSelectedNamespace(undefined); inventoryMutation.reset() }} destroyOnHidden
      >
        <Alert className="environment-modal-alert" showIcon type="info" title="只保存资源元数据、数据键名和依赖关系；Secret 与 ConfigMap 的值不会进入 Inventory。" />
        <Form layout="vertical"><Form.Item label="Namespace" required>
          <Select showSearch value={selectedNamespace} loading={namespaces.isPending} placeholder="选择待迁移 Namespace" options={(namespaces.data ?? []).map((name) => ({ label: name, value: name }))} onChange={setSelectedNamespace} />
        </Form.Item></Form>
        {namespaces.isError && <Alert showIcon type="error" title="无法读取 Namespace" description={errorMessage(namespaces.error)} />}
        {inventoryMutation.isError && <Alert showIcon type="error" title="发现失败" description={errorMessage(inventoryMutation.error)} />}
      </Modal>
      <KubernetesInventoryDrawer application={inventoryResult} targets={(environments.data ?? []).filter((item) => item.role === 'TARGET')} preview={preview} onClose={() => setInventoryResult(undefined)} />
      <Drawer
        title={`${capabilityResult?.name ?? ''} · 能力快照`} size={560} open={Boolean(capabilityResult)}
        onClose={() => setCapabilityResult(undefined)} destroyOnHidden
      >
        {capabilityResult && <CapabilitySnapshot capabilities={capabilityResult.capabilities} />}
      </Drawer>
      <Modal
        title={`${connectionResult?.name ?? ''} · 连接检查`} open={Boolean(connectionResult)} footer={null}
        onCancel={() => setConnectionResult(undefined)} destroyOnHidden
      >
        <div className="connection-checks">
          {connectionResult?.result.checks.map((check) => (
            <div className="connection-check" key={check.name}>
              {check.status === 'PASSED' ? <CheckCircleOutlined className="semantic-success" /> : check.status === 'WARNING' ? <ExclamationCircleOutlined className="semantic-warning" /> : <DisconnectOutlined className="semantic-error" />}
              <span><Text strong>{check.name}</Text><small>{check.message}</small></span>
            </div>
          ))}
        </div>
      </Modal>
    </>
  )
}

function EnvironmentStatusTag({ status }: { status: Environment['status'] }) {
  const values = {
    CONNECTED: { label: '已连接', className: 'status-success', icon: <CheckCircleOutlined /> },
    PENDING: { label: '待测试', className: 'status-attention', icon: <ExclamationCircleOutlined /> },
    DISCONNECTED: { label: '连接失败', className: 'status-error', icon: <DisconnectOutlined /> },
    ERROR: { label: '异常', className: 'status-error', icon: <ExclamationCircleOutlined /> },
  }
  const value = values[status]
  return <Tag className={`status-tag ${value.className}`} icon={value.icon}>{value.label}</Tag>
}

function errorMessage(error: unknown) {
  return error instanceof APIError || error instanceof Error ? error.message : '请求失败'
}

function CapabilitySnapshot({ capabilities }: { capabilities: ClusterCapabilities }) {
  const tagList = (values?: string[]) => values?.length ? <Space size={[4, 6]} wrap>{values.map((value) => <Tag key={value}>{value}</Tag>)}</Space> : <Text type="secondary">未发现</Text>
  const mover = capabilities.csiDataMover
  if (!capabilities.kubernetesVersion && capabilities.runtime) {
    const runtime = capabilities.runtime
    const memory = Number(runtime.memoryBytes)
    return <div className="capability-snapshot">
      <Descriptions column={2} size="small" bordered items={[
        { key: 'docker', label: 'Docker Engine', children: runtime.dockerVersion || '—' },
        { key: 'compose', label: 'Docker Compose', children: runtime.composeVersion || '—' },
        { key: 'projects', label: 'Compose 项目', children: runtime.projectCount ?? '—' },
        { key: 'architecture', label: '主机架构', children: runtime.architecture || '—' },
        { key: 'os', label: '操作系统', children: runtime.operatingSystem || '—', span: 2 },
        { key: 'cpu', label: 'CPU', children: runtime.cpus || '—' },
        { key: 'memory', label: '内存', children: Number.isFinite(memory) && memory > 0 ? `${(memory / 1024 / 1024 / 1024).toFixed(1)} GiB` : '—' },
        { key: 'root', label: 'Docker 数据目录', children: runtime.dockerRootDir || '—', span: 2 },
        { key: 'driver', label: '存储驱动', children: runtime.storageDriver || '—' },
        { key: 'cgroup', label: 'Cgroup Driver', children: runtime.cgroupDriver || '—' },
      ]} />
      <Alert className="page-alert" showIcon type="info" title="发现应用时会读取 docker compose ls 的项目、状态、Compose 文件路径和工作目录。" />
    </div>
  }
  return (
    <div className="capability-snapshot">
      <Descriptions column={2} size="small" bordered items={[
        { key: 'version', label: 'Kubernetes', children: capabilities.kubernetesVersion || '—', span: 2 },
        { key: 'nodes', label: '节点', children: capabilities.nodeCount ?? 0 },
        { key: 'namespaces', label: '命名空间', children: capabilities.namespaceCount ?? 0 },
        { key: 'cpu', label: '可分配 CPU', children: capabilities.allocatable?.cpu ?? '—' },
        { key: 'memory', label: '可分配内存', children: capabilities.allocatable?.memory ?? '—' },
      ]} />
      <CapabilityGroup title="节点架构">{tagList(capabilities.architectures)}</CapabilityGroup>
      <CapabilityGroup title="节点操作系统">{tagList(capabilities.operatingSystems)}</CapabilityGroup>
      <CapabilityGroup title="CSI Driver">{tagList(capabilities.csiDrivers)}</CapabilityGroup>
      <CapabilityGroup title="VolumeSnapshotClass">
        {capabilities.volumeSnapshotClassDetails?.length
          ? <Space size={[4, 6]} wrap>{capabilities.volumeSnapshotClassDetails.map((value) => <Tag key={value.name}>{value.name} · {value.driver}</Tag>)}</Space>
          : tagList(capabilities.volumeSnapshotClasses)}
      </CapabilityGroup>
      <CapabilityGroup title="CSI Data Mover">
        <Space size={[4, 6]} wrap>
          <Tag color={mover?.backupReady ? 'success' : 'default'}>源端上传 {mover?.backupReady ? '就绪' : '不可用'}</Tag>
          <Tag color={mover?.restoreReady ? 'success' : 'default'}>目标端下载 {mover?.restoreReady ? '就绪' : '不可用'}</Tag>
          <Tag color={mover?.enableCsi ? 'blue' : 'default'}>EnableCSI</Tag>
          <Tag color={mover?.nodeAgentDesired && mover.nodeAgentReady === mover.nodeAgentDesired ? 'success' : 'default'}>
            node-agent {mover?.nodeAgentReady ?? 0}/{mover?.nodeAgentDesired ?? 0}
          </Tag>
        </Space>
      </CapabilityGroup>
      <CapabilityGroup title="IngressClass">{tagList(capabilities.ingressClasses)}</CapabilityGroup>
      <CapabilityGroup title="StorageClass">
        <Table
          size="small" rowKey="name" pagination={false} dataSource={capabilities.storageClasses ?? []}
          locale={{ emptyText: '未发现 StorageClass' }} columns={[
            { title: '名称', dataIndex: 'name', render: (value, record) => <Space>{value}{record.default && <Tag color="blue">默认</Tag>}</Space> },
            { title: 'Provisioner', dataIndex: 'provisioner', ellipsis: true },
            { title: '扩容', dataIndex: 'allowExpansion', width: 62, render: (value) => value ? '支持' : '否' },
          ]}
        />
      </CapabilityGroup>
      <CapabilityGroup title={`API Groups (${capabilities.apiGroups?.length ?? 0})`}>
        <Text type="secondary">{capabilities.apiGroups?.join('、') || '未发现'}</Text>
      </CapabilityGroup>
    </div>
  )
}

function CapabilityGroup({ title, children }: { title: string; children: ReactNode }) {
  return <section className="capability-group"><Text strong>{title}</Text><div>{children}</div></section>
}

const previewEnvironments: Environment[] = [
  {
    id: 'preview-source', name: '生产 Kubernetes', role: 'SOURCE', kind: 'KUBERNETES',
    endpoint: 'https://10.20.30.40:6443', status: 'CONNECTED', statusMessage: '连接正常',
    capabilities: { kubernetesVersion: 'v1.31.9', nodeCount: 6, namespaceCount: 18 },
    capabilitiesUpdatedAt: '2026-09-02T14:00:00Z', createdAt: '2026-09-02T13:00:00Z', updatedAt: '2026-09-02T14:00:00Z',
  },
  {
    id: 'preview-target', name: 'SKS-Production', role: 'TARGET', kind: 'KUBERNETES',
    endpoint: 'https://10.60.10.20:6443', status: 'CONNECTED', statusMessage: '连接正常',
    capabilities: {
      kubernetesVersion: 'v1.32.6', architectures: ['amd64'], nodeCount: 5, namespaceCount: 14,
      allocatable: { cpu: '80', memory: '300Gi', pods: '550' }, csiDrivers: ['smtx-elf-csi-driver'],
      volumeSnapshotClasses: ['smtx-elf-snapshot'], ingressClasses: ['nginx'], apiGroups: ['apps', 'batch', 'networking.k8s.io', 'storage.k8s.io', 'v1'],
      storageClasses: [{ name: 'smtx-elf-storageclass', provisioner: 'smtx-elf-csi-driver', default: true, allowExpansion: true, volumeBindingMode: 'WaitForFirstConsumer' }],
    },
    capabilitiesUpdatedAt: '2026-09-02T14:10:00Z', createdAt: '2026-09-02T13:30:00Z', updatedAt: '2026-09-02T14:10:00Z',
  },
]

const previewApplication: SourceApplication = {
  id: 'preview-application', environmentId: 'preview-source', name: 'business', namespace: 'business', sourceType: 'KUBERNETES',
  createdAt: '2026-09-03T00:00:00Z', updatedAt: '2026-09-03T00:00:00Z',
  inventory: {
    resources: [
      { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'business', name: 'api', images: ['registry.example/api:v1'] },
      { apiVersion: 'v1', kind: 'Service', namespace: 'business', name: 'api' },
      { apiVersion: 'v1', kind: 'Secret', namespace: 'business', name: 'database', secretKeys: ['password', 'username'] },
    ],
    workloads: [{ apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'business', name: 'api', images: ['registry.example/api:v1'] }],
    services: [{ apiVersion: 'v1', kind: 'Service', namespace: 'business', name: 'api' }], ingresses: [], configMaps: [],
    secrets: [{ apiVersion: 'v1', kind: 'Secret', namespace: 'business', name: 'database', secretKeys: ['password', 'username'] }],
    serviceAccounts: [], roles: [], roleBindings: [], crds: [], customResources: [],
    pvcs: [{ name: 'api-data', namespace: 'business', capacityBytes: 10737418240, storageClassName: 'smtx-block', accessModes: ['ReadWriteOnce'], volumeMode: 'Filesystem' }],
    images: [{ reference: 'registry.example/api:v1' }],
    dependencies: [{ from: { apiVersion: 'v1', kind: 'Service', namespace: 'business', name: 'api' }, to: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'business', name: 'api' }, type: 'SELECTS', required: true }],
    warnings: [], counts: { Deployment: 1, Service: 1, Secret: 1 },
  },
}
