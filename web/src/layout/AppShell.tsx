import { useState, type PropsWithChildren } from 'react'
import {
  ApartmentOutlined, AppstoreOutlined, CloudServerOutlined, DatabaseOutlined, DeploymentUnitOutlined,
  ExportOutlined, HddOutlined, KeyOutlined, LockOutlined, LogoutOutlined,
  SafetyCertificateOutlined, SettingOutlined, SwapOutlined, DownOutlined,
} from '@ant-design/icons'
import { App as AntApplication, Avatar, Button, Dropdown, Form, Input, Layout, Menu, Modal, Typography, type MenuProps } from 'antd'
import { useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate } from 'react-router-dom'
import { APIError, changePassword, logout, type Administrator } from '../api/client'

const { Header, Content, Sider } = Layout

const menuItems: MenuProps['items'] = [
  { key: '/', icon: <AppstoreOutlined />, label: '概览' },
  { key: '/migrations', icon: <SwapOutlined />, label: '迁移任务' },
  {
    key: 'environment', icon: <CloudServerOutlined />, label: '环境',
    children: [
      { key: '/environments/sources', label: '源环境' },
      { key: '/environments/sks', label: 'SmartX SKS' },
    ],
  },
  {
    key: 'strategy', icon: <DeploymentUnitOutlined />, label: '迁移策略',
    children: [
      { key: '/strategies/storage', icon: <ApartmentOutlined />, label: '资源映射' },
      { key: '/strategies/registry', icon: <ExportOutlined />, label: '镜像仓库' },
      { key: '/strategies/transforms', icon: <SafetyCertificateOutlined />, label: '资源转换规则' },
    ],
  },
  {
    key: 'settings', icon: <SettingOutlined />, label: '系统设置',
    children: [
      { key: '/settings/storage-profiles', icon: <HddOutlined />, label: '存储配置' },
      { key: '/settings/object-storage', icon: <DatabaseOutlined />, label: '对象存储' },
      { key: '/settings/credentials', icon: <KeyOutlined />, label: '凭证' },
    ],
  },
]

type AppShellProps = PropsWithChildren<{ administrator: Administrator }>
type ChangePasswordFields = { currentPassword: string; newPassword: string; confirmPassword: string }

export default function AppShell({ administrator, children }: AppShellProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { message } = AntApplication.useApp()
  const [passwordForm] = Form.useForm<ChangePasswordFields>()
  const [passwordDialogOpen, setPasswordDialogOpen] = useState(false)
  const [passwordChanging, setPasswordChanging] = useState(false)
  const selected = location.pathname.startsWith('/migrations/') ? '/migrations' : location.pathname
  const accountItems: MenuProps['items'] = [
    { key: 'change-password', icon: <LockOutlined />, label: '修改密码' },
    { type: 'divider' },
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录' },
  ]

  async function handleAccountAction({ key }: { key: string }) {
    if (key === 'change-password') {
      setPasswordDialogOpen(true)
      return
    }
    if (key === 'logout') {
      await logout()
      queryClient.removeQueries({ queryKey: ['current-administrator'] })
      navigate('/login', { replace: true })
    }
  }

  function closePasswordDialog() {
    if (passwordChanging) return
    setPasswordDialogOpen(false)
    passwordForm.resetFields()
  }

  async function submitPasswordChange(values: ChangePasswordFields) {
    setPasswordChanging(true)
    try {
      await changePassword(values.currentPassword, values.newPassword)
      setPasswordDialogOpen(false)
      passwordForm.resetFields()
      queryClient.removeQueries({ queryKey: ['current-administrator'] })
      message.success('密码已修改，请使用新密码重新登录')
      navigate('/login', { replace: true })
    } catch (error) {
      if (error instanceof APIError && error.code === 'AUTH_CURRENT_PASSWORD_INVALID') {
        passwordForm.setFields([{ name: 'currentPassword', errors: ['当前密码不正确'] }])
      } else if (error instanceof APIError && error.code === 'AUTH_PASSWORD_TOO_SHORT') {
        passwordForm.setFields([{ name: 'newPassword', errors: ['新密码至少需要 12 个字符'] }])
      } else if (error instanceof APIError && error.code === 'AUTH_PASSWORD_UNCHANGED') {
        passwordForm.setFields([{ name: 'newPassword', errors: ['新密码不能与当前密码相同'] }])
      } else {
        message.error(error instanceof Error ? error.message : '密码修改失败')
      }
    } finally {
      setPasswordChanging(false)
    }
  }

  return (
    <Layout className="application-shell">
      <Sider className="application-sidebar" width={216} theme="light">
        <button className="brand" type="button" onClick={() => navigate('/')} aria-label="SKS Migration Center 首页">
          <img className="brand-symbol" src="/brand-logo.png" alt="" />
          <span className="brand-copy"><strong>SKS</strong><small>Migration Center</small></span>
        </button>
        <Menu
          mode="inline" items={menuItems} selectedKeys={[selected]}
          defaultOpenKeys={['environment', 'strategy', 'settings']}
          onClick={({ key }) => key.startsWith('/') && navigate(key)}
        />
        <div className="sidebar-footnote">面向工作负载集群的迁移中心</div>
      </Sider>
      <Layout>
        <Header className="application-header">
          <Dropdown menu={{ items: accountItems, onClick: handleAccountAction }} placement="bottomRight" trigger={['click']}>
            <Button type="text" className="account-button">
              <Avatar size={28}>{administrator.username.slice(0, 1).toUpperCase()}</Avatar>
              <span>{administrator.username}</span><DownOutlined style={{ fontSize: 10 }} />
            </Button>
          </Dropdown>
        </Header>
        <Content className="application-content"><main key={location.pathname} className="page-surface">{children}</main></Content>
      </Layout>
      <Modal
        title="修改管理员密码" open={passwordDialogOpen} okText="确认修改" cancelText="取消"
        confirmLoading={passwordChanging} mask={{ closable: !passwordChanging }}
        onOk={() => passwordForm.submit()} onCancel={closePasswordDialog} destroyOnHidden
      >
        <Form<ChangePasswordFields> form={passwordForm} layout="vertical" onFinish={submitPasswordChange} autoComplete="off">
          <Form.Item name="currentPassword" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}>
            <Input.Password autoComplete="current-password" placeholder="输入当前密码" />
          </Form.Item>
          <Form.Item name="newPassword" label="新密码" rules={[{ required: true, message: '请输入新密码' }, { min: 12, message: '新密码至少需要 12 个字符' }]}>
            <Input.Password autoComplete="new-password" placeholder="至少 12 个字符" />
          </Form.Item>
          <Form.Item
            name="confirmPassword" label="确认新密码" dependencies={['newPassword']}
            rules={[
              { required: true, message: '请再次输入新密码' },
              ({ getFieldValue }) => ({ validator: (_, value) => !value || getFieldValue('newPassword') === value ? Promise.resolve() : Promise.reject(new Error('两次输入的新密码不一致')) }),
            ]}
          >
            <Input.Password autoComplete="new-password" placeholder="再次输入新密码" />
          </Form.Item>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>修改成功后，当前及其他已登录会话都会退出。</Typography.Paragraph>
        </Form>
      </Modal>
    </Layout>
  )
}
