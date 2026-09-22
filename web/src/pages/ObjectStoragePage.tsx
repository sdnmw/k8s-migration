import { useEffect, useMemo, useState } from 'react'
import { App, Alert, Button, Card, Col, Form, Input, Popconfirm, Row, Select, Space, Table, Tabs, Tag, Typography } from 'antd'
import Modal from '../components/EditorModal'
import { CloudUploadOutlined, DeleteOutlined, LinkOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import PageHeader from '../components/PageHeader'
import {
  adoptManagedMinIO, bootstrapMinIO, connectExternalS3, getMinIOSourcePolicy, getVeleroStatus, installVelero, listAddonStatuses, listEnvironments,
  listObjectStorageProfiles, reuseVelero, testMinIO, type AddonInstallation, type ExternalS3Input, type MinIOAdoptInput, type MinIOBootstrapInput,
  uninstallVelero,
} from '../api/client'

const { Text } = Typography

type VeleroFormValue = { environmentId: string; profileId: string; kubeletRoot: string; prefix: string }

export default function ObjectStoragePage({ preview = false }: { preview?: boolean }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [minioForm] = Form.useForm<MinIOBootstrapInput>()
  const [externalForm] = Form.useForm<ExternalS3Input>()
  const [veleroForm] = Form.useForm<VeleroFormValue>()
  const [minioOpen, setMinioOpen] = useState(false)
  const [externalOpen, setExternalOpen] = useState(false)
  const environments = useQuery({
    queryKey: ['environments'], queryFn: listEnvironments, enabled: !preview,
    initialData: preview ? previewEnvironments : undefined,
  })
  const profiles = useQuery({
    queryKey: ['object-storage-profiles'], queryFn: listObjectStorageProfiles, enabled: !preview,
    initialData: preview ? previewProfiles : undefined,
  })
  const policy = useQuery({ queryKey: ['minio-source-policy'], queryFn: getMinIOSourcePolicy, enabled: !preview })
  const selectedEnvironment = Form.useWatch('environmentId', veleroForm)
  const selectedMinIOEnvironment = Form.useWatch('environmentId', minioForm)
  const statuses = useQuery({
    queryKey: ['addon-statuses', selectedEnvironment],
    queryFn: () => listAddonStatuses(selectedEnvironment!), enabled: !preview && Boolean(selectedEnvironment),
    initialData: preview ? previewAddons : undefined,
  })
  const veleroHealth = useQuery({
    queryKey: ['velero-health', selectedEnvironment],
    queryFn: () => getVeleroStatus(selectedEnvironment!), enabled: !preview && Boolean(selectedEnvironment),
  })
  const minioStatuses = useQuery({
    queryKey: ['addon-statuses', selectedMinIOEnvironment],
    queryFn: () => listAddonStatuses(selectedMinIOEnvironment!), enabled: !preview && Boolean(selectedMinIOEnvironment),
    initialData: preview ? previewAddons : undefined,
  })
  const targets = useMemo(() => environments.data?.filter((value) => value.role === 'TARGET' && value.kind === 'KUBERNETES' && value.status === 'CONNECTED') ?? [], [environments.data])
  const kubernetesEnvironments = useMemo(() => environments.data?.filter((value) => value.kind === 'KUBERNETES' && value.status === 'CONNECTED') ?? [], [environments.data])
  useEffect(() => {
    if (!policy.data) return
    minioForm.setFieldsValue({
      imageRepository: policy.data.officialImage.repository,
      imageDigest: policy.data.officialImage.digest,
    })
  }, [minioForm, policy.data])
  useEffect(() => {
    if (!selectedMinIOEnvironment && targets.length === 1) {
      minioForm.setFieldValue('environmentId', targets[0].id)
    }
  }, [minioForm, selectedMinIOEnvironment, targets])
  useEffect(() => {
    if (!selectedMinIOEnvironment) return
    const target = targets.find((value) => value.id === selectedMinIOEnvironment)
    const smartxClasses = target?.capabilities.storageClasses?.filter((value) => value.provisioner === 'smtx-elf-csi-driver' || value.provisioner === 'com.smartx.elf-csi-driver') ?? []
    const preferred = smartxClasses.find((value) => value.default) ?? smartxClasses[0]
    minioForm.setFieldValue('storageClass', preferred?.name)
  }, [minioForm, selectedMinIOEnvironment, targets])

  const minioInstallation = minioStatuses.data?.find((value) => value.type === 'MINIO')
  const statusRows = useMemo(() => (statuses.data ?? []).map((value) => {
    if (value.type !== 'VELERO' || !veleroHealth.data || veleroHealth.data.health === 'READY') return value
    const failedCheck = veleroHealth.data.checks.find((check) => check.status === 'FAILED')
    return { ...value, status: 'FAILED' as const, message: failedCheck?.message ?? '当前集群中的迁移组件需要修复' }
  }), [statuses.data, veleroHealth.data])

  const bootstrap = useMutation({
    mutationFn: bootstrapMinIO,
    onSuccess: async (result) => {
      message.success(`MinIO 已就绪：${result.profile.endpoint}`)
	  setMinioOpen(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['object-storage-profiles'] }),
        queryClient.invalidateQueries({ queryKey: ['addon-statuses'] }),
      ])
    },
    onError: (error: Error) => message.error(error.message),
  })
  const adopt = useMutation({
    mutationFn: adoptManagedMinIO,
    onSuccess: async (result) => {
      message.success(`已接管现有 MinIO：${result.profile.endpoint}`)
	  setMinioOpen(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['object-storage-profiles'] }),
        queryClient.invalidateQueries({ queryKey: ['addon-statuses'] }),
      ])
    },
    onError: (error: Error) => message.error(error.message),
  })
  const connectExternal = useMutation({
    mutationFn: connectExternalS3,
    onSuccess: async (result) => {
      message.success(`外部 S3 已连接：${result.profile.endpoint}`)
      setExternalOpen(false)
      externalForm.resetFields()
      await queryClient.invalidateQueries({ queryKey: ['object-storage-profiles'] })
    },
    onError: (error: Error) => message.error(error.message),
  })
  const verify = useMutation({
    mutationFn: testMinIO,
    onSuccess: () => message.success('S3 写入、读取和校验通过'),
    onError: (error: Error) => message.error(error.message),
  })
  const install = useMutation({
    mutationFn: (value: VeleroFormValue) => installVelero(value.environmentId, value.profileId, value.kubeletRoot, value.prefix),
    onSuccess: async (result) => {
      message.success(`Velero ${result.installation.version} 已就绪，BSL ${result.backupStorageLocation.phase}`)
      await queryClient.invalidateQueries({ queryKey: ['addon-statuses'] })
      await queryClient.invalidateQueries({ queryKey: ['velero-health'] })
    },
    onError: (error: Error) => message.error(error.message),
  })
  const reuse = useMutation({
    mutationFn: (value: VeleroFormValue) => reuseVelero(value.environmentId, value.profileId, value.prefix),
    onSuccess: async (result) => {
      message.success(`已复用 Velero ${result.installation.version}，迁移 BSL ${result.backupStorageLocation.phase}`)
      await queryClient.invalidateQueries({ queryKey: ['addon-statuses'] })
      await queryClient.invalidateQueries({ queryKey: ['velero-health'] })
    },
    onError: (error: Error) => message.error(error.message),
  })
  const uninstall = useMutation({
    mutationFn: uninstallVelero,
    onSuccess: async () => {
      message.success('Velero Helm release 已卸载，MinIO 数据仍保留')
      await queryClient.invalidateQueries({ queryKey: ['addon-statuses'] })
      await queryClient.invalidateQueries({ queryKey: ['velero-health'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const profileTable = <Table rowKey="id" pagination={false} loading={profiles.isLoading} dataSource={profiles.data ?? []} columns={[
    { title: '名称', dataIndex: 'name' },
    { title: 'S3 Endpoint', dataIndex: 'endpoint' },
    { title: 'Bucket', dataIndex: 'bucket' },
    { title: 'TLS', dataIndex: 'tlsVerify', render: (value: boolean) => <Tag color={value ? 'success' : 'error'}>{value ? '校验' : '关闭'}</Tag> },
    { title: '操作', key: 'action', render: (_, value) => <Button size="small" loading={verify.isPending} onClick={() => verify.mutate(value.id)}>读写测试</Button> },
  ]} />

  return <>
    <PageHeader title="对象存储与 Velero" description="在目标 SKS 工作负载集群部署单实例 MinIO，并为源端和目标端安装 Velero 1.18、node-agent 与 Kopia。" />
    <Alert className="page-alert" showIcon type="info" title="Velero 直接通过 Kubernetes CR 工作；MinIO 与 Velero 镜像均固定 digest，离线部署时可映射到客户 Harbor。" />
    <Tabs items={[
      { key: 'minio', label: 'MinIO', children: <Space orientation="vertical" size={16} style={{ width: '100%' }}>
        <Card title="已有对象存储" extra={<Space><Button icon={<PlusOutlined />} onClick={() => setMinioOpen(true)}>新增 MinIO</Button><Button icon={<LinkOutlined />} onClick={() => setExternalOpen(true)}>对接其他 S3</Button></Space>}>{profileTable}</Card>
        {minioInstallation?.status === 'FAILED' && <Alert showIcon type="warning" title="上次部署未完成" description="可以重试部署；如果目标集群中的 sks-migration-minio 已在运行，请使用“接管并验证现有 MinIO”，不会重建 StatefulSet 或 PVC。" />}
        {minioInstallation?.status === 'READY' && <Alert showIcon type="success" title="该目标集群的 MinIO 已就绪" description={minioInstallation.message} />}
        {!selectedMinIOEnvironment && targets.length > 0 && <Alert showIcon type="info" title="默认 MinIO 会随平台一键部署并显示在上表" description="只有需要独立 Bucket、独立容量或使用客户已有 S3 时，才需要使用右上角两个入口。" />}
        <Modal title="新增或接管 MinIO" open={minioOpen} width={980} okButtonProps={{ style: { display: 'none' } }} cancelText="关闭" onCancel={() => setMinioOpen(false)} forceRender>
          <Form form={minioForm} layout="vertical" onFinish={(value) => bootstrap.mutate(value)} initialValues={{
            name: 'managed-minio', bucket: 'velero', region: 'minio', storageSize: '100Gi',
            imageRepository: policy.data?.officialImage.repository ?? 'docker.io/minio/minio',
            imageDigest: policy.data?.officialImage.digest, tlsSecretName: 'sks-migration-minio-tls',
          }}>
            <Row gutter={16}>
              <Col span={8}><Form.Item name="environmentId" label="目标 SKS" rules={[{ required: true }]}><Select options={targets.map((value) => ({ value: value.id, label: value.name }))} /></Form.Item></Col>
              <Col span={8}><Form.Item name="storageClass" label="SmartX 块 StorageClass" rules={[{ required: true }]}><Input placeholder="smtx-elf-csi-driver" /></Form.Item></Col>
              <Col span={8}><Form.Item name="storageSize" label="容量" rules={[{ required: true }]}><Input /></Form.Item></Col>
            </Row>
            <Row gutter={16}>
              <Col span={12}><Form.Item name="endpoint" label="源集群可达的 HTTPS S3 Endpoint" rules={[{ required: true }, { type: 'url' }]}><Input placeholder="https://192.168.1.10:30900" /></Form.Item></Col>
              <Col span={6}><Form.Item name="bucket" label="Bucket"><Input /></Form.Item></Col>
              <Col span={6}><Form.Item name="region" label="Region"><Input /></Form.Item></Col>
            </Row>
            <Row gutter={16}>
              <Col span={10}><Form.Item name="imageRepository" label="官方镜像或 Harbor 映射" rules={[{ required: true }]}><Input /></Form.Item></Col>
              <Col span={14}><Form.Item name="imageDigest" label="镜像 Digest" rules={[{ required: true }]}><Input /></Form.Item></Col>
            </Row>
            <Form.Item name="tlsSecretName" label="目标集群 TLS Secret" rules={[{ required: true }]}><Input /></Form.Item>
            <Form.Item name="caBundle" label="CA 证书（PEM，可选）"><Input.TextArea autoSize={{ minRows: 3, maxRows: 7 }} /></Form.Item>
            <Space>
              <Button type="primary" htmlType="submit" icon={<CloudUploadOutlined />} loading={bootstrap.isPending} disabled={minioInstallation?.status === 'READY'}>{minioInstallation?.status === 'FAILED' ? '重试部署并验证' : minioInstallation?.status === 'READY' ? 'MinIO 已部署' : '部署并执行持久化验证'}</Button>
              <Button loading={adopt.isPending} disabled={!selectedMinIOEnvironment || minioInstallation?.status === 'INSTALLING'} onClick={() => minioForm.validateFields(['environmentId', 'name', 'endpoint', 'bucket', 'region', 'tlsSecretName', 'caBundle']).then((value) => adopt.mutate(value as MinIOAdoptInput))}>接管并验证现有 MinIO</Button>
            </Space>
          </Form>
        </Modal>
        <Modal title="对接其他 S3" open={externalOpen} width={760} okText="连接并验证" cancelText="取消" confirmLoading={connectExternal.isPending} onOk={() => externalForm.submit()} onCancel={() => setExternalOpen(false)} destroyOnHidden>
          <Alert className="page-alert" showIcon type="info" title="支持兼容 S3 API 的对象存储" description="提交前会执行对象写入、读取和 SHA-256 校验；Access Key 与 Secret Key 不会回显。" />
          <Form form={externalForm} layout="vertical" initialValues={{ region: 'us-east-1' }} onFinish={(value) => connectExternal.mutate(value)}>
            <Row gutter={16}>
              <Col span={12}><Form.Item name="name" label="配置名称" rules={[{ required: true }]}><Input placeholder="例如：customer-s3" /></Form.Item></Col>
              <Col span={12}><Form.Item name="endpoint" label="HTTPS S3 Endpoint" rules={[{ required: true }, { type: 'url' }]}><Input placeholder="https://s3.example.com" /></Form.Item></Col>
            </Row>
            <Row gutter={16}>
              <Col span={12}><Form.Item name="bucket" label="Bucket" rules={[{ required: true }]}><Input /></Form.Item></Col>
              <Col span={12}><Form.Item name="region" label="Region" rules={[{ required: true }]}><Input /></Form.Item></Col>
            </Row>
            <Row gutter={16}>
              <Col span={12}><Form.Item name="accessKey" label="Access Key" rules={[{ required: true }]}><Input /></Form.Item></Col>
              <Col span={12}><Form.Item name="secretKey" label="Secret Key" rules={[{ required: true }]}><Input.Password /></Form.Item></Col>
            </Row>
            <Form.Item name="caBundle" label="私有 CA 证书（PEM，可选）"><Input.TextArea autoSize={{ minRows: 3, maxRows: 7 }} /></Form.Item>
          </Form>
        </Modal>
      </Space> },
      { key: 'velero', label: 'Velero / Kopia', children: <Space orientation="vertical" size={16} style={{ width: '100%' }}>
        <Card title="安装到集群">
          <Form form={veleroForm} layout="vertical" onFinish={(value) => install.mutate(value)} initialValues={{
            environmentId: preview ? 'preview-source' : undefined,
            profileId: preview ? 'preview-minio' : undefined,
            kubeletRoot: '/var/lib/kubelet',
            prefix: 'demo-migration',
          }}>
            <Row gutter={16}>
              <Col span={6}><Form.Item name="environmentId" label="集群" rules={[{ required: true }]}><Select options={kubernetesEnvironments.map((value) => ({ value: value.id, label: `${value.name} · ${value.role === 'SOURCE' ? '源端' : '目标端'}` }))} /></Form.Item></Col>
              <Col span={6}><Form.Item name="profileId" label="MinIO Profile" rules={[{ required: true }]}><Select options={(profiles.data ?? []).map((value) => ({ value: value.id, label: `${value.name} · ${value.bucket}` }))} /></Form.Item></Col>
              <Col span={6}><Form.Item name="prefix" label="仓库前缀" tooltip="源端与目标端必须保持一致" rules={[{ required: true }, { pattern: /^(?!\/)(?!.*\.\.)(?!.*\\)[^\r\n]+$/, message: '不能以 /开头，且不能包含 .. 或反斜杠' }]}><Input placeholder="demo-migration" /></Form.Item></Col>
              <Col span={6}><Form.Item name="kubeletRoot" label="Kubelet Root" rules={[{ required: true }, { pattern: /^\/(?!.*\.\.)/, message: '必须是绝对路径' }]}><Input /></Form.Item></Col>
            </Row>
            <Space>
              <Button type="primary" htmlType="submit" icon={<CloudUploadOutlined />} loading={install.isPending}>安装 Velero 1.18</Button>
              <Button onClick={() => veleroForm.validateFields().then((value) => reuse.mutate(value))} loading={reuse.isPending}>复用已有 Velero</Button>
            </Space>
          </Form>
        </Card>
        <Card title="组件状态" extra={<Button icon={<ReloadOutlined />} disabled={!selectedEnvironment} onClick={() => { statuses.refetch(); veleroHealth.refetch() }}>刷新</Button>}>
          {!selectedEnvironment && <Text type="secondary">选择集群后显示 MinIO、NFS CSI 和 Velero 状态。</Text>}
          {selectedEnvironment && veleroHealth.data && veleroHealth.data.health !== 'READY' && <Alert
            className="page-alert" showIcon type={veleroHealth.data?.health === 'CHECK_FAILED' ? 'warning' : 'error'}
            title={veleroHealth.data?.health === 'NOT_INSTALLED' ? '该集群尚未安装迁移组件' : '当前集群中的迁移组件需要修复'}
            description={veleroHealth.data?.checks.find((check) => check.status === 'FAILED')?.message ?? '请实时检查 Velero、node-agent 和 migration-minio BSL。'}
          />}
          {selectedEnvironment && <Table rowKey="id" pagination={false} loading={statuses.isLoading || veleroHealth.isLoading} dataSource={statusRows} columns={[...addonColumns, {
            title: '操作', key: 'action', render: (_, value: AddonInstallation) => value.type === 'VELERO' && value.status !== 'REMOVED' && value.values?.managed === true
              ? <Popconfirm title="卸载受管 Velero？" description="请先结束迁移和回滚窗口。MinIO 数据与凭证 Secret 将保留。" okText="卸载" cancelText="取消" onConfirm={() => uninstall.mutate(value.environmentId)}><Button danger size="small" icon={<DeleteOutlined />} loading={uninstall.isPending}>卸载</Button></Popconfirm>
              : value.type === 'VELERO' && value.values?.mode === 'REUSED' ? <Tag>外部管理</Tag> : '—',
          }]} />}
        </Card>
      </Space> },
    ]} />
  </>
}

const addonColumns = [
  { title: '组件', dataIndex: 'type' },
  { title: '版本', dataIndex: 'version' },
  { title: '状态', dataIndex: 'status', render: (value: AddonInstallation['status']) => <Tag color={value === 'READY' ? 'success' : value === 'FAILED' ? 'error' : 'processing'}>{value}</Tag> },
  { title: '说明', dataIndex: 'message', render: (value?: string) => value || '—' },
]

const previewEnvironments = [
  { id: 'preview-source', name: 'sida', role: 'SOURCE' as const, kind: 'KUBERNETES' as const, status: 'CONNECTED' as const, capabilities: {}, createdAt: '', updatedAt: '' },
  { id: 'preview-target', name: 'mw', role: 'TARGET' as const, kind: 'KUBERNETES' as const, status: 'CONNECTED' as const, capabilities: {}, createdAt: '', updatedAt: '' },
]
const previewProfiles = [{ id: 'preview-minio', name: 'managed-minio', endpoint: 'https://192.168.112.121:30900', bucket: 'velero', region: 'minio', tlsVerify: true, createdAt: '', updatedAt: '' }]
const previewAddons: AddonInstallation[] = [{
  id: 'preview-velero', environmentId: 'preview-source', type: 'VELERO', version: '1.13.2', status: 'READY',
  values: { managed: false, mode: 'REUSED' }, message: '复用集群已有 Velero 和 node-agent；迁移专用 MinIO BSL 已就绪', createdAt: '', updatedAt: '',
}]
