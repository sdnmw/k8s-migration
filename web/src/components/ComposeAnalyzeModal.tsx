import { useState } from 'react'
import { FileTextOutlined, InboxOutlined } from '@ant-design/icons'
import { Alert, App as AntApplication, Button, Descriptions, Form, Input, Select, Space, Table, Tag, Typography, Upload } from 'antd'
import Modal from './EditorModal'
import { useMutation } from '@tanstack/react-query'
import { registerCompose, APIError, type ComposeInventory, type Environment } from '../api/client'

const { Text } = Typography

export default function ComposeAnalyzeModal({ open, environments, onClose }: { open: boolean; environments: Environment[]; onClose: () => void }) {
  const { message } = AntApplication.useApp()
  const [form] = Form.useForm<{ environmentId: string; projectName?: string }>()
  const [composeFile, setComposeFile] = useState<File>()
  const [environmentFile, setEnvironmentFile] = useState<File>()
  const [inventory, setInventory] = useState<ComposeInventory>()
  const mutation = useMutation({
    mutationFn: ({ environmentId, projectName }: { environmentId: string; projectName?: string }) => registerCompose(environmentId, composeFile as File, environmentFile, projectName),
    onSuccess: (value) => {
      setInventory(value.inventory.compose)
      message.success(`应用 ${value.name} 已注册，可进入评估与迁移计划`)
    },
  })
  const close = () => {
    form.resetFields(); mutation.reset(); setComposeFile(undefined); setEnvironmentFile(undefined); setInventory(undefined); onClose()
  }
  return (
    <Modal width={820} title="注册 Docker Compose 应用" open={open} footer={null} onCancel={close} destroyOnHidden>
      <Alert showIcon type="info" title="完整定义将关联到所选 Compose 主机；Inventory 和界面只展示环境变量键名。外部 include、env_file、config 和 secret 文件引用会被拒绝。" />
      <Form form={form} layout="vertical" className="compose-analyze-form" onFinish={(values) => {
        if (!composeFile) { message.error('请选择 compose.yaml'); return }
        mutation.mutate(values)
      }}>
        <Form.Item name="environmentId" label="Compose 源主机" rules={[{ required: true, message: '请选择已连接的 Compose 源主机' }]}>
          <Select placeholder="选择源主机" options={environments.map((item) => ({ label: item.name, value: item.id, disabled: item.status !== 'CONNECTED' }))} />
        </Form.Item>
        <Form.Item name="projectName" label="项目名称（可选）"><Input placeholder="migration" maxLength={128} /></Form.Item>
        <Space align="start" wrap>
          <Upload.Dragger
            accept=".yaml,.yml" maxCount={1} fileList={composeFile ? [{ uid: 'compose', name: composeFile.name, status: 'done' }] : []}
            beforeUpload={(file) => { setComposeFile(file); return Upload.LIST_IGNORE }} onRemove={() => { setComposeFile(undefined); return true }}
          ><p className="ant-upload-drag-icon"><InboxOutlined /></p><p>选择 compose.yaml</p><Text type="secondary">最大 2 MiB</Text></Upload.Dragger>
          <Upload.Dragger
            maxCount={1} fileList={environmentFile ? [{ uid: 'env', name: environmentFile.name, status: 'done' }] : []}
            beforeUpload={(file) => { setEnvironmentFile(file); return Upload.LIST_IGNORE }} onRemove={() => { setEnvironmentFile(undefined); return true }}
          ><p className="ant-upload-drag-icon"><FileTextOutlined /></p><p>选择 .env（可选）</p><Text type="secondary">最大 1 MiB</Text></Upload.Dragger>
        </Space>
        {mutation.isError && <Alert showIcon type="error" title="解析失败" description={errorMessage(mutation.error)} />}
        {environments.length === 0 && <Alert showIcon type="warning" title="请先添加 Docker Compose 源环境并通过连接测试。" />}
        <Button type="primary" htmlType="submit" loading={mutation.isPending} disabled={!composeFile || environments.length === 0}>分析并注册</Button>
      </Form>
      {inventory && <ComposeInventoryView value={inventory} />}
    </Modal>
  )
}

function ComposeInventoryView({ value }: { value: ComposeInventory }) {
  return <section className="compose-inventory">
    <Descriptions bordered size="small" column={4} items={[
      { key: 'project', label: '项目', children: value.projectName },
      { key: 'services', label: '服务', children: value.services.length },
      { key: 'volumes', label: '数据卷', children: value.volumes.length },
      { key: 'warnings', label: '风险', children: value.warnings.length },
    ]} />
    {value.warnings.length > 0 && <Alert showIcon type="warning" title="需要评估" description={value.warnings.map((warning) => `${warning.service ? `${warning.service}: ` : ''}${warning.message}`).join('；')} />}
    <Table size="small" rowKey="name" pagination={false} dataSource={value.services} columns={[
      { title: '服务', dataIndex: 'name' },
      { title: '镜像', dataIndex: 'image', ellipsis: true, render: (image: string | undefined, record) => image || (record.build ? <Tag color="warning">仅 build</Tag> : '—') },
      { title: '端口', dataIndex: 'ports', render: (ports) => ports.length ? ports.map((port: { published?: string; target: number }) => `${port.published || '—'}:${port.target}`).join('、') : '—' },
      { title: '挂载', dataIndex: 'mounts', render: (mounts) => mounts.length ? mounts.map((mount: { type: string; target: string }) => `${mount.type}:${mount.target}`).join('、') : '—' },
      { title: '环境变量', dataIndex: 'environmentKeys', render: (keys) => keys.length ? `${keys.length} 个键` : '—' },
    ]} />
  </section>
}

function errorMessage(error: unknown) {
  return error instanceof APIError || error instanceof Error ? error.message : '请求失败'
}
