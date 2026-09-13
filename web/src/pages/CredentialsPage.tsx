import { useState } from 'react'
import { DeleteOutlined, KeyOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App as AntApplication, Button, Card, Form, Input, Popconfirm, Select, Space, Table, Tag, Typography } from 'antd'
import Modal from '../components/EditorModal'
import { createCredential, deleteCredential, listCredentials, type CredentialType } from '../api/client'
import PageHeader from '../components/PageHeader'

const { Text } = Typography
type CredentialForm = { name: string; type: CredentialType; payload: string }
const typeLabels: Record<CredentialType, string> = { KUBECONFIG: 'Kubernetes kubeconfig', SSH: 'SSH 凭证 JSON', REGISTRY: '镜像仓库凭证 JSON', S3: 'S3 凭证 JSON' }

export default function CredentialsPage() {
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm<CredentialForm>()
  const queryClient = useQueryClient()
  const { message } = AntApplication.useApp()
  const values = useQuery({ queryKey: ['credentials'], queryFn: listCredentials })
  const createMutation = useMutation({
    mutationFn: (input: CredentialForm) => createCredential(input.name.trim(), input.type, input.payload),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['credentials'] }); setOpen(false); form.resetFields(); message.success('凭证已加密保存') },
    onError: (error) => message.error(error instanceof Error ? error.message : '凭证保存失败'),
  })
  const deleteMutation = useMutation({
    mutationFn: deleteCredential,
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ['credentials'] }); message.success('凭证已删除') },
    onError: (error) => message.error(error instanceof Error ? error.message : '凭证删除失败；请确认没有环境或配置正在引用它'),
  })
  const credentialType = Form.useWatch('type', form)
  return <>
    <PageHeader title="凭证" description="集中管理 kubeconfig、SSH、Registry 与 S3 凭证；内容只写入、不可从 API 或界面回读。" action={<Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>添加凭证</Button>} />
    <Alert className="page-alert" showIcon type="info" title="列表只显示凭证元数据。已被环境、对象存储或镜像仓库引用的凭证不能直接删除。" />
    <Card className="section-card" variant="outlined">
      <Table rowKey="id" loading={values.isPending} dataSource={values.data ?? []} locale={{ emptyText: '尚未添加独立凭证' }} pagination={{ pageSize: 10, hideOnSinglePage: true }} columns={[
        { title: '名称', dataIndex: 'name', render: (value: string) => <Space><KeyOutlined className="environment-kind-icon" /><Text strong>{value}</Text></Space> },
        { title: '类型', dataIndex: 'type', width: 220, render: (value: CredentialType) => <Tag>{typeLabels[value]}</Tag> },
        { title: '创建时间', dataIndex: 'createdAt', width: 220, render: (value: string) => new Date(value).toLocaleString('zh-CN') },
        { title: '操作', width: 100, render: (_, record) => <Popconfirm title="删除凭证？" description="删除后无法恢复。" onConfirm={() => deleteMutation.mutate(record.id)}><Button type="link" danger icon={<DeleteOutlined />}>删除</Button></Popconfirm> },
      ]} />
    </Card>
    <Modal title="添加凭证" open={open} okText="加密保存" cancelText="取消" confirmLoading={createMutation.isPending} onOk={() => form.submit()} onCancel={() => { setOpen(false); form.resetFields() }} destroyOnHidden>
      <Alert className="environment-modal-alert" showIcon type="warning" title="保存后无法回显凭证内容，请保留原始凭证。" />
      <Form<CredentialForm> form={form} layout="vertical" onFinish={(input) => createMutation.mutate(input)} initialValues={{ type: 'REGISTRY' }}>
        <Form.Item name="name" label="凭证名称" rules={[{ required: true, whitespace: true, message: '请输入凭证名称' }, { max: 128 }]}><Input placeholder="例如：目标 Harbor" autoComplete="off" /></Form.Item>
        <Form.Item name="type" label="凭证类型" rules={[{ required: true }]}><Select options={Object.entries(typeLabels).map(([value, label]) => ({ value, label }))} /></Form.Item>
        <Form.Item name="payload" label="凭证内容" extra={credentialType === 'KUBECONFIG' ? '粘贴完整 kubeconfig YAML。' : '粘贴 JSON，例如 Registry: {"username":"...","password":"..."}。'} rules={[{ required: true, message: '请输入凭证内容' }]}><Input.TextArea rows={9} autoComplete="off" spellCheck={false} placeholder={credentialType === 'KUBECONFIG' ? 'apiVersion: v1\nkind: Config' : '{"username":"...","password":"..."}'} /></Form.Item>
      </Form>
    </Modal>
  </>
}
