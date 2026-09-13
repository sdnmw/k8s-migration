import type { ReactNode } from 'react'
import { Button, Flex, Typography } from 'antd'
import { LeftOutlined } from '@ant-design/icons'
import { useLocation, useNavigate } from 'react-router-dom'

const { Paragraph, Title } = Typography

type PageHeaderProps = { title: string; description?: string; action?: ReactNode }

export default function PageHeader({ title, description, action }: PageHeaderProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const nested = location.pathname.startsWith('/migrations/') || location.pathname.startsWith('/__preview/detail')
  return (
    <Flex justify="space-between" align="flex-start" className="page-header">
      <div>
        {nested && <Button type="text" size="small" icon={<LeftOutlined />} className="page-back" onClick={() => navigate('/migrations')}>迁移任务</Button>}
        <Title level={1}>{title}</Title>
        {description && <Paragraph>{description}</Paragraph>}
      </div>
      {action && <div className="page-header-actions">{action}</div>}
    </Flex>
  )
}
