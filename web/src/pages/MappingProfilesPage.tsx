import { useState } from 'react'
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App as AntApplication, Button, Card, Form, Input, Popconfirm, Select, Space, Table, Tabs, Tag } from 'antd'
import Modal from '../components/EditorModal'
import type { ColumnsType } from 'antd/es/table'
import {
  createMappingProfile, deleteMappingProfile, listEnvironments, listMappingProfiles, updateMappingProfile,
  type Environment, type MappingProfile, type MappingProfileInput,
} from '../api/client'
import PageHeader from '../components/PageHeader'

const emptyProfile = (): MappingProfileInput => ({ name: '', targetEnvironmentId: '', storageMappings: [], namespaceMappings: [], ingressMappings: [], registryMappings: [], nfsMappings: [], nodeLabelMappings: [] })

export default function MappingProfilesPage({ initialTab = 'storage', preview = false }: { initialTab?: 'storage' | 'registry'; preview?: boolean }) {
  const { message } = AntApplication.useApp()
  const [form] = Form.useForm<MappingProfileInput>()
  const [editing, setEditing] = useState<MappingProfile>()
  const [open, setOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<string>(initialTab)
  const registryMode = initialTab === 'registry'
  const queryClient = useQueryClient()
  const environments = useQuery({ queryKey: ['environments'], queryFn: listEnvironments, enabled: !preview, initialData: preview ? previewTargets : undefined })
  const profiles = useQuery({ queryKey: ['mapping-profiles'], queryFn: () => listMappingProfiles(), enabled: !preview, initialData: preview ? previewProfiles : undefined })
  const targets = (environments.data ?? []).filter((item) => item.role === 'TARGET' && item.kind === 'KUBERNETES')
  const selectedTargetID = Form.useWatch('targetEnvironmentId', form)
  const selectedTarget = targets.find((item) => item.id === selectedTargetID)
  const save = useMutation({
    mutationFn: (value: MappingProfileInput) => preview ? Promise.resolve({ ...value, id: editing?.id ?? 'preview-new', createdAt: editing?.createdAt ?? new Date().toISOString(), updatedAt: new Date().toISOString() }) : editing ? updateMappingProfile(editing.id, value) : createMappingProfile(value),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['mapping-profiles'] }); closeEditor(); message.success(editing ? '映射配置已更新' : '映射配置已创建') },
  })
  const remove = useMutation({ mutationFn: deleteMappingProfile, onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['mapping-profiles'] }); message.success('映射配置已删除') } })

  const columns: ColumnsType<MappingProfile> = [
    { title: '名称', dataIndex: 'name', render: (value, record) => <Button type="link" className="table-link" onClick={() => edit(record)}>{value}</Button> },
    { title: '目标 SKS', dataIndex: 'targetEnvironmentId', render: (id) => targets.find((item) => item.id === id)?.name ?? id },
    { title: registryMode ? '镜像仓库规则' : '资源映射规则', key: 'counts', render: (_, item) => <Space size={4} wrap>{mappingCounts(item).filter(([label]) => registryMode ? label === 'Registry' : label !== 'Registry').map(([label, count]) => <Tag key={label}>{label} {count}</Tag>)}</Space> },
    { title: '更新时间', dataIndex: 'updatedAt', width: 180, render: (value) => new Date(value).toLocaleString('zh-CN', { hour12: false }) },
    { title: '操作', key: 'actions', width: 112, render: (_, item) => <Space size={2}><Button type="text" aria-label={`编辑 ${item.name}`} icon={<EditOutlined />} onClick={() => edit(item)} /><Popconfirm title="删除映射配置？" description="已被迁移计划引用时系统会拒绝删除。" onConfirm={() => remove.mutate(item.id)}><Button type="text" danger aria-label={`删除 ${item.name}`} icon={<DeleteOutlined />} /></Popconfirm></Space> },
  ]

  function edit(value?: MappingProfile) {
    setEditing(value); setOpen(true); setActiveTab(initialTab)
    form.setFieldsValue(value ? profileInput(value) : emptyProfile())
  }
  function closeEditor() { setOpen(false); setEditing(undefined); form.resetFields(); save.reset() }

  return <>
    <PageHeader title={registryMode ? '镜像仓库映射' : '资源映射'} description={registryMode ? '配置源镜像 Registry 前缀到目标 Harbor 项目或仓库的映射，迁移时按规则改写镜像地址。' : '维护源 StorageClass、外部 NFS、Namespace、IngressClass 和 NodeLabel 到目标 SKS 的映射。'} action={<Button type="primary" icon={<PlusOutlined />} onClick={() => edit()}>{registryMode ? '创建镜像映射' : '创建资源映射'}</Button>} />
    <Alert className="page-alert" showIcon type="info" title={registryMode ? '镜像映射只改写仓库前缀；镜像存在性、AMD64 架构和目标 Harbor 可达性会在 Preflight 中检查。' : '映射目标来自所选 SKS 工作负载集群的能力快照；StorageClass、Namespace 冲突和重复源规则会被阻止。'} />
    {(profiles.isError || environments.isError) && <Alert className="page-alert" showIcon type="error" title="无法读取映射配置" />}
    <Card className="section-card environment-table-card" variant="outlined"><Table<MappingProfile> rowKey="id" columns={columns} dataSource={profiles.data ?? []} loading={profiles.isPending || environments.isPending} locale={{ emptyText: '尚未创建映射配置' }} /></Card>
    <Modal title={editing ? `编辑${registryMode ? '镜像仓库' : '资源'}映射 · ${editing.name}` : `创建${registryMode ? '镜像仓库' : '资源'}映射`} open={open} width={1000} style={{ top: 24 }} okText="保存" cancelText="取消" confirmLoading={save.isPending} onOk={() => form.submit()} onCancel={closeEditor} destroyOnHidden>
      <Form<MappingProfileInput> form={form} layout="vertical" initialValues={emptyProfile()} onFinish={(value) => save.mutate(value)}>
        <div className="mapping-profile-basics">
          <Form.Item name="name" label="配置名称" rules={[{ required: true, whitespace: true }, { max: 128 }]}><Input placeholder="例如：SKS-Production 默认映射" /></Form.Item>
          <Form.Item name="targetEnvironmentId" label="目标 SKS 工作负载集群" rules={[{ required: true }]}><Select placeholder="选择目标集群" options={targets.map((target) => ({ value: target.id, label: target.name }))} /></Form.Item>
        </div>
        <Tabs activeKey={activeTab} onChange={setActiveTab} items={(registryMode ? [
          { key: 'registry', label: 'Registry', children: <PairList name="registryMappings" sourceLabel="源 Registry 前缀" targetLabel="目标 Harbor 前缀" /> },
        ] : [
          { key: 'storage', label: 'StorageClass', children: <PairList name="storageMappings" sourceLabel="源 StorageClass" targetLabel="目标 StorageClass" targetOptions={selectedTarget?.capabilities.storageClasses?.map((item) => item.name)} /> },
          { key: 'nfs', label: '外部 NFS', children: <NFSList storageClasses={selectedTarget?.capabilities.storageClasses?.map((item) => item.name) ?? []} /> },
          { key: 'namespace', label: 'Namespace', children: <PairList name="namespaceMappings" sourceLabel="源 Namespace" targetLabel="目标 Namespace" /> },
          { key: 'ingress', label: 'IngressClass', children: <PairList name="ingressMappings" sourceLabel="源 IngressClass" targetLabel="目标 IngressClass" targetOptions={selectedTarget?.capabilities.ingressClasses} /> },
          { key: 'node', label: 'NodeLabel', children: <NodeLabelList /> },
        ])} />
        {save.isError && <Alert showIcon type="error" title="无法保存映射" description={save.error instanceof Error ? save.error.message : '请求失败'} />}
      </Form>
    </Modal>
  </>
}

function PairList({ name, sourceLabel, targetLabel, targetOptions }: { name: 'storageMappings' | 'namespaceMappings' | 'ingressMappings' | 'registryMappings'; sourceLabel: string; targetLabel: string; targetOptions?: string[] }) {
  return <Form.List name={name}>{(fields, { add, remove }) => <div className="mapping-list">
    {fields.map((field) => <div className="mapping-row" key={field.key}>
      <Form.Item name={[field.name, 'source']} label={sourceLabel} rules={[{ required: true }]}><Input /></Form.Item>
      <span className="mapping-arrow">→</span>
      <Form.Item name={[field.name, 'target']} label={targetLabel} rules={[{ required: true }]}>{targetOptions ? <Select showSearch options={targetOptions.map((value) => ({ value }))} /> : <Input />}</Form.Item>
      <Button type="text" danger icon={<DeleteOutlined />} aria-label="删除映射项" onClick={() => remove(field.name)} />
    </div>)}
    <Button type="dashed" icon={<PlusOutlined />} onClick={() => add({ source: '', target: '' })}>添加映射项</Button>
  </div>}</Form.List>
}

function NFSList({ storageClasses }: { storageClasses: string[] }) {
  return <Form.List name="nfsMappings">{(fields, { add, remove }) => <div className="mapping-list">
    {fields.map((field) => <Card size="small" key={field.key} extra={<Button type="text" danger icon={<DeleteOutlined />} onClick={() => remove(field.name)} />}>
      <div className="nfs-mapping-grid">
        <Form.Item name={[field.name, 'sourceServer']} label="源 NFS Server" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name={[field.name, 'sourceExport']} label="源 Export" rules={[{ required: true }, { pattern: /^\//, message: '必须是绝对路径' }]}><Input placeholder="/data" /></Form.Item>
        <Form.Item name={[field.name, 'targetServer']} label="目标 NFS Server" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name={[field.name, 'targetExport']} label="目标 Export" rules={[{ required: true }, { pattern: /^\//, message: '必须是绝对路径' }]}><Input placeholder="/migrations" /></Form.Item>
        <Form.Item name={[field.name, 'targetStorageClass']} label="目标 StorageClass" rules={[{ required: true }]}><Select showSearch options={storageClasses.map((value) => ({ value }))} /></Form.Item>
      </div>
    </Card>)}
    <Button type="dashed" icon={<PlusOutlined />} onClick={() => add({})}>添加 NFS 映射</Button>
  </div>}</Form.List>
}

function NodeLabelList() {
  return <Form.List name="nodeLabelMappings">{(fields, { add, remove }) => <div className="mapping-list">
    {fields.map((field) => <div className="node-mapping-row" key={field.key}>
      <Form.Item name={[field.name, 'source']} label="源 NodeLabel" rules={[{ required: true }]}><Input /></Form.Item>
      <Form.Item name={[field.name, 'action']} label="动作" rules={[{ required: true }]}><Select options={[{ value: 'MAP', label: '映射' }, { value: 'DROP', label: '删除约束' }]} /></Form.Item>
      <Form.Item noStyle shouldUpdate>{({ getFieldValue }) => getFieldValue(['nodeLabelMappings', field.name, 'action']) === 'DROP' ? <div /> : <Form.Item name={[field.name, 'target']} label="目标 NodeLabel" rules={[{ required: true }]}><Input /></Form.Item>}</Form.Item>
      <Button type="text" danger icon={<DeleteOutlined />} aria-label="删除 NodeLabel 映射" onClick={() => remove(field.name)} />
    </div>)}
    <Button type="dashed" icon={<PlusOutlined />} onClick={() => add({ action: 'MAP' })}>添加 NodeLabel 映射</Button>
  </div>}</Form.List>
}

function profileInput(value: MappingProfile): MappingProfileInput { return { name: value.name, targetEnvironmentId: value.targetEnvironmentId, storageMappings: value.storageMappings, namespaceMappings: value.namespaceMappings, ingressMappings: value.ingressMappings, registryMappings: value.registryMappings, nfsMappings: value.nfsMappings, nodeLabelMappings: value.nodeLabelMappings } }
function mappingCounts(value: MappingProfile): Array<[string, number]> { return [['SC', value.storageMappings.length], ['NFS', value.nfsMappings.length], ['NS', value.namespaceMappings.length], ['Ingress', value.ingressMappings.length], ['Registry', value.registryMappings.length], ['Label', value.nodeLabelMappings.length]] }

const previewTargets: Environment[] = [{ id: 'preview-target', name: 'SKS-Production', role: 'TARGET', kind: 'KUBERNETES', endpoint: 'https://10.60.10.20:6443', status: 'CONNECTED', capabilities: { kubernetesVersion: 'v1.32.6', storageClasses: [{ name: 'smtx-elf-storageclass', provisioner: 'smtx-elf-csi-driver', default: true, allowExpansion: true }, { name: 'nfs-rwx', provisioner: 'nfs.csi.k8s.io', default: false, allowExpansion: true }], ingressClasses: ['nginx'] }, capabilitiesUpdatedAt: '2026-09-03T06:00:00Z', createdAt: '2026-09-03T05:00:00Z', updatedAt: '2026-09-03T06:00:00Z' }]
const previewProfiles: MappingProfile[] = [{ id: 'preview-profile', name: '生产默认映射', targetEnvironmentId: 'preview-target', storageMappings: [{ source: 'legacy-block', target: 'smtx-elf-storageclass' }], namespaceMappings: [{ source: 'business', target: 'business-prod' }], ingressMappings: [{ source: 'traefik', target: 'nginx' }], registryMappings: [{ source: 'registry.legacy.local', target: 'harbor.example.local/migrated' }], nfsMappings: [{ sourceServer: '10.20.30.60', sourceExport: '/business', targetServer: '10.60.30.60', targetExport: '/migrations/business', targetStorageClass: 'nfs-rwx' }], nodeLabelMappings: [{ source: 'legacy/rack', target: 'topology.kubernetes.io/zone', action: 'MAP' }], createdAt: '2026-09-03T05:30:00Z', updatedAt: '2026-09-03T06:30:00Z' }]
