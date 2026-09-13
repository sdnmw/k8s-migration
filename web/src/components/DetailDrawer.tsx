import { useState } from 'react'
import { Button, Drawer, Space, Tooltip, type DrawerProps } from 'antd'
import { FullscreenExitOutlined, FullscreenOutlined } from '@ant-design/icons'

export default function DetailDrawer({ size = 720, extra, onClose, ...props }: DrawerProps) {
  const [expanded, setExpanded] = useState(false)
  return <Drawer {...props} size={expanded ? 'calc(100vw - 48px)' : size} onClose={onClose} extra={<Space>{extra}<Tooltip title={expanded ? '还原宽度' : '展开详情'}><Button type="text" aria-label={expanded ? '还原宽度' : '展开详情'} icon={expanded ? <FullscreenExitOutlined /> : <FullscreenOutlined />} onClick={() => setExpanded(!expanded)} /></Tooltip></Space>} footer={props.footer ?? <Button onClick={onClose}>关闭</Button>} />
}
