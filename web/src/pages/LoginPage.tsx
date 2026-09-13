import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { Alert, Button, Card, Form, Input, Typography } from 'antd'
import { useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate } from 'react-router-dom'
import { login } from '../api/client'

const { Text, Title } = Typography
type LoginFields = { username: string; password: string }

export default function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [form] = Form.useForm<LoginFields>()
  const from = (location.state as { from?: string } | null)?.from ?? '/'

  const submit = async (values: LoginFields) => {
    try {
      await login(values.username, values.password)
      await queryClient.invalidateQueries({ queryKey: ['current-administrator'] })
      navigate(from, { replace: true })
    } catch {
      form.setFields([{ name: 'password', errors: ['用户名或密码不正确'] }])
    }
  }

  return (
    <main className="login-page">
      <section className="login-brand-panel">
        <img className="login-brand-mark" src="/brand-logo.png" alt="SKS Migration Center" />
        <Title level={1}>SKS Migration Center</Title>
        <Text>面向 SmartX SKS 工作负载集群的应用迁移工作台</Text>
      </section>
      <Card className="login-card" variant="outlined">
        <Title level={2}>管理员登录</Title>
        <Text type="secondary">使用本地管理员账号进入迁移中心</Text>
        <Form<LoginFields> form={form} layout="vertical" requiredMark={false} onFinish={submit} initialValues={{ username: 'admin' }}>
          <Form.Item label="用户名" name="username" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input prefix={<UserOutlined />} autoComplete="username" />
          </Form.Item>
          <Form.Item label="密码" name="password" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password prefix={<LockOutlined />} autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block>登录</Button>
        </Form>
        <Alert type="info" showIcon title="该系统仅支持一个本地管理员账号。" />
      </Card>
    </main>
  )
}
