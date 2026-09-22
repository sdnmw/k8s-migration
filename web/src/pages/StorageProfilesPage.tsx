import { useState } from 'react'
import { CheckCircleOutlined, ExperimentOutlined, PlusOutlined, ToolOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App as AntApplication, Button, Card, Form, Input, Select, Space, Table, Tag, Typography } from 'antd'
import Modal from '../components/EditorModal'
import type { ColumnsType } from 'antd/es/table'
import {
  createStorageProfile, installStorageProfile, listEnvironments, listStorageProfiles, testStorageProfile,
  type Environment, type StorageProfile, type StorageProfileInput,
} from '../api/client'
import PageHeader from '../components/PageHeader'

type EditorValue = Omit<StorageProfileInput, 'mountOptions'> & { mountOptionsText?: string }

const typeLabels: Record<StorageProfile['type'], string> = {
  SMTX_BLOCK: 'SmartX 块存储', EXISTING_NFS_SC: '已有 NFS StorageClass', EXTERNAL_NFS_SC: '新建外部 NFS',
}

export default function StorageProfilesPage({ preview = false }: { preview?: boolean }) {
  const { message } = AntApplication.useApp()
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm<EditorValue>()
  const queryClient = useQueryClient()
  const environments = useQuery({ queryKey: ['environments'], queryFn: listEnvironments, enabled: !preview, initialData: preview ? previewEnvironments : undefined })
  const profiles = useQuery({ queryKey: ['storage-profiles'], queryFn: () => listStorageProfiles(), enabled: !preview, initialData: preview ? previewProfiles : undefined })
  const targets = (environments.data ?? []).filter((item) => item.kind === 'KUBERNETES' && item.status === 'CONNECTED')
  const selectedEnvironmentID = Form.useWatch('environmentId', form)
  const selectedType = Form.useWatch('type', form)
  const selectedEnvironment = targets.find((item) => item.id === selectedEnvironmentID)
  const existingNFSClasses = selectedEnvironment?.capabilities.storageClasses?.filter((item) => item.provisioner === 'nfs.csi.k8s.io') ?? []
  const save = useMutation({
    mutationFn: (value: EditorValue) => createStorageProfile({
      ...value,
      mountOptions: value.mountOptionsText?.split(',').map((item) => item.trim()).filter(Boolean),
    }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['storage-profiles'] })
      setOpen(false); form.resetFields(); message.success('存储配置已创建')
    },
  })
  const install = useMutation({
    mutationFn: installStorageProfile,
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: ['storage-profiles'] })
      message.success(`${result.profile.storageClassName} 安装和重新挂载读写探测通过`)
    },
    onError: (error) => message.error(error instanceof Error ? error.message : '安装探测失败'),
  })
  const probe = useMutation({
    mutationFn: testStorageProfile,
    onSuccess: (result) => message.success(`${result.profile.storageClassName} 写入和重新挂载读取通过`),
    onError: (error) => message.error(error instanceof Error ? error.message : '读写探测失败'),
  })

  const columns: ColumnsType<StorageProfile> = [
    { title: '名称', dataIndex: 'name', render: (value) => <Typography.Text strong>{value}</Typography.Text> },
    { title: '集群', dataIndex: 'environmentId', render: (id) => targets.find((item) => item.id === id)?.name ?? id },
    { title: '类型', dataIndex: 'type', render: (value) => typeLabels[value as StorageProfile['type']] },
    { title: 'StorageClass', dataIndex: 'storageClassName' },
    { title: 'NFS Server / Export', key: 'nfs', render: (_, value) => value.nfsServer ? `${value.nfsServer}:${value.nfsExport}` : '—' },
    { title: '回收策略', dataIndex: 'reclaimPolicy', width: 92, render: (value) => <Tag color={value === 'Retain' ? 'blue' : 'default'}>{value}</Tag> },
    { title: '状态', dataIndex: 'status', width: 100, render: profileStatus },
    { title: '操作', key: 'actions', width: 286, render: (_, value) => value.type === 'SMTX_BLOCK' ? '—' : <Space size={4}>
      <Button size="small" icon={<ToolOutlined />} loading={install.isPending && install.variables === value.id} onClick={() => install.mutate(value.id)}>准备存储并验证</Button>
      <Button size="small" icon={<ExperimentOutlined />} disabled={value.status !== 'READY'} loading={probe.isPending && probe.variables === value.id} onClick={() => probe.mutate(value.id)}>重新验证读写</Button>
    </Space> },
  ]

  function openEditor() {
    form.setFieldsValue({ type: 'EXTERNAL_NFS_SC', reclaimPolicy: 'Retain' })
    setOpen(true)
  }

  return <>
    <PageHeader title="存储配置" description="管理 SmartX 块存储和外部 NFS；NFS 会从集群内部完成动态供给、写入、卸载、重新挂载读取验证。" action={<Button type="primary" icon={<PlusOutlined />} onClick={openEditor}>创建存储配置</Button>} />
    <Alert className="page-alert" showIcon type="info" title="检测到已有 nfs.csi.k8s.io 时直接复用；缺失时从离线包安装 4.13.4。新建外部 NFS 默认使用 Retain 回收策略。" />
    {(profiles.isError || environments.isError) && <Alert className="page-alert" showIcon type="error" title="无法读取存储配置" />}
    <Card className="section-card environment-table-card" variant="outlined">
      <Table<StorageProfile> rowKey="id" columns={columns} dataSource={profiles.data ?? []} loading={profiles.isPending || environments.isPending} locale={{ emptyText: '尚未创建存储配置' }} />
    </Card>
    <Modal title="创建存储配置" open={open} width={720} okText="创建" cancelText="取消" confirmLoading={save.isPending} onOk={() => form.submit()} onCancel={() => { setOpen(false); form.resetFields(); save.reset() }} destroyOnHidden>
      <Form<EditorValue> form={form} layout="vertical" initialValues={{ type: 'EXTERNAL_NFS_SC', reclaimPolicy: 'Retain' }} onFinish={(value) => save.mutate(value)}>
        <div className="storage-profile-grid">
          <Form.Item name="name" label="配置名称" rules={[{ required: true, whitespace: true }]}><Input placeholder="例如：目标共享文件存储" /></Form.Item>
          <Form.Item name="environmentId" label="Kubernetes 集群" rules={[{ required: true }]}><Select placeholder="选择集群" options={targets.map((value: Environment) => ({ value: value.id, label: `${value.name} · ${value.role === 'TARGET' ? '目标' : '源'}` }))} /></Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}><Select options={[
            { value: 'EXTERNAL_NFS_SC', label: '新建外部 NFS StorageClass' },
            { value: 'EXISTING_NFS_SC', label: '复用已有 NFS StorageClass' },
            { value: 'SMTX_BLOCK', label: 'SmartX 块 StorageClass' },
          ]} /></Form.Item>
          <Form.Item name="reclaimPolicy" label="回收策略" rules={[{ required: true }]}><Select options={[{ value: 'Retain', label: 'Retain（推荐）' }, { value: 'Delete', label: 'Delete' }]} /></Form.Item>
        </div>
        {selectedType === 'EXISTING_NFS_SC' && <Form.Item name="storageClassName" label="已有 NFS StorageClass" rules={[{ required: true }]}><Select placeholder="选择集群已发现的 NFS StorageClass" options={existingNFSClasses.map((value) => ({ value: value.name, label: value.name }))} /></Form.Item>}
        {selectedType === 'SMTX_BLOCK' && <Form.Item name="storageClassName" label="SmartX 块 StorageClass" rules={[{ required: true }]}><Select options={(selectedEnvironment?.capabilities.storageClasses ?? []).filter((value) => value.provisioner.includes('smartx') || value.provisioner.includes('smtx')).map((value) => ({ value: value.name, label: value.name }))} /></Form.Item>}
        {selectedType === 'EXTERNAL_NFS_SC' && <>
          <Form.Item name="storageClassName" label="新 StorageClass 名称" rules={[{ required: true }, { pattern: /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/, message: '请输入小写 DNS 名称' }]}><Input placeholder="sks-migration-nfs" /></Form.Item>
          <div className="storage-profile-grid">
            <Form.Item name="nfsServer" label="NFS Server" rules={[{ required: true }]}><Input placeholder="10.20.30.40" /></Form.Item>
            <Form.Item name="nfsExport" label="NFS Export" rules={[{ required: true }, { pattern: /^\/(?!.*\.\.)/, message: '必须是无父目录跳转的绝对路径' }]}><Input placeholder="/migration" /></Form.Item>
          </div>
          <Form.Item name="mountOptionsText" label="Mount Options" extra="使用英文逗号分隔；留空时使用 NFS Server 默认协商。"><Input placeholder="例如：nfsvers=4.1,hard,timeo=600,retrans=2" /></Form.Item>
        </>}
        {save.isError && <Alert showIcon type="error" title="无法创建存储配置" description={save.error instanceof Error ? save.error.message : '请求失败'} />}
      </Form>
    </Modal>
  </>
}

function profileStatus(value: StorageProfile['status']) {
  const config = {
    READY: { color: 'success', text: '就绪', icon: <CheckCircleOutlined /> },
    INSTALLING: { color: 'processing', text: '安装中' }, PENDING: { color: 'default', text: '待安装' }, FAILED: { color: 'error', text: '失败' },
  }[value]
  return <Tag color={config.color} icon={config.icon}>{config.text}</Tag>
}

const previewEnvironments: Environment[] = [{
  id: 'preview-sks', name: 'mw', role: 'TARGET', kind: 'KUBERNETES', status: 'CONNECTED', endpoint: 'https://192.168.118.206:6443',
  capabilities: { storageClasses: [{ name: 'smtx-elf-csi-driver', provisioner: 'com.smartx.elf-csi-driver', default: true, allowExpansion: true }, { name: 'sks-migration-nfs', provisioner: 'nfs.csi.k8s.io', default: false, allowExpansion: true }] },
  createdAt: '2026-09-04T04:00:00Z', updatedAt: '2026-09-04T04:30:00Z',
}]
const previewProfiles: StorageProfile[] = [
  { id: 'preview-block', environmentId: 'preview-sks', name: 'SmartX 默认块存储', type: 'SMTX_BLOCK', storageClassName: 'smtx-elf-csi-driver', provisioner: 'com.smartx.elf-csi-driver', reclaimPolicy: 'Retain', status: 'READY', createdAt: '2026-09-04T04:00:00Z', updatedAt: '2026-09-04T04:00:00Z' },
  { id: 'preview-nfs', environmentId: 'preview-sks', name: '迁移共享文件存储', type: 'EXTERNAL_NFS_SC', storageClassName: 'sks-migration-nfs', nfsServer: '20.20.20.71', nfsExport: '/sfs-velero', mountOptions: [], provisioner: 'nfs.csi.k8s.io', reclaimPolicy: 'Retain', status: 'READY', createdAt: '2026-09-04T04:20:00Z', updatedAt: '2026-09-04T04:34:00Z' },
]
