import { Modal, type ModalProps } from 'antd'

/** Shared editor surface: the form scrolls while title and actions stay visible. */
export default function EditorModal({ className, ...props }: ModalProps) {
  return <Modal {...props} className={`editor-modal ${className ?? ''}`} centered styles={{ ...props.styles, body: { maxHeight: 'calc(100dvh - 230px)', overflowY: 'auto', paddingInlineEnd: 8 } }} />
}
