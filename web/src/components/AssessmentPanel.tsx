import { useMemo, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Empty, Row, Select, Space, Statistic, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { createAssessment, type Assessment, type AssessmentIssue, type Environment, type SourceApplication } from '../api/client'

const { Text } = Typography

type Props = {
  application: SourceApplication
  targets: Environment[]
  preview?: boolean
}

export default function AssessmentPanel({ application, targets, preview = false }: Props) {
  const eligibleTargets = useMemo(() => targets.filter((target) => target.role === 'TARGET' && target.kind === 'KUBERNETES' && target.status === 'CONNECTED' && target.capabilitiesUpdatedAt), [targets])
  const [targetID, setTargetID] = useState<string>()
  const mutation = useMutation({
    mutationFn: () => preview ? Promise.resolve(previewAssessment(application.id)) : createAssessment(application.id, targetID ?? ''),
  })
  const result = mutation.data

  return <div className="assessment-panel">
    <Space.Compact block>
      <Select
        aria-label="目标 SKS 工作负载集群" showSearch optionFilterProp="label" value={targetID}
        placeholder="选择已完成能力发现的目标 SKS 集群"
        options={eligibleTargets.map((target) => ({ value: target.id, label: `${target.name} · ${target.capabilities.kubernetesVersion ?? '版本未知'}` }))}
        onChange={(value) => { setTargetID(value); mutation.reset() }}
      />
      <Button type="primary" disabled={!targetID} loading={mutation.isPending} onClick={() => mutation.mutate()}>开始评估</Button>
    </Space.Compact>
    {eligibleTargets.length === 0 && <Alert showIcon type="warning" title="没有可评估的目标集群" description="请先导入目标 SKS 工作负载集群，完成连接测试和能力发现。" />}
    {mutation.isError && <Alert showIcon type="error" title="评估失败" description={mutation.error instanceof Error ? mutation.error.message : '请求失败'} />}
    {!result && eligibleTargets.length > 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择目标集群后运行迁移评估" />}
    {result && <AssessmentResult value={result} />}
  </div>
}

function AssessmentResult({ value }: { value: Assessment }) {
  const columns: ColumnsType<AssessmentIssue> = [
    { title: '级别', dataIndex: 'severity', width: 84, render: (severity) => <Tag className={`assessment-severity severity-${String(severity).toLowerCase()}`}>{severityLabel(severity)}</Tag> },
    { title: '类别', dataIndex: 'category', width: 90, render: categoryLabel },
    { title: '资源', key: 'resource', width: 170, ellipsis: true, render: (_, issue) => `${issue.resourceKind}/${issue.resourceNamespace ? `${issue.resourceNamespace}/` : ''}${issue.resourceName}` },
    { title: '问题与建议', key: 'issue', render: (_, issue) => <span className="assessment-issue"><Text strong>{issue.title}</Text><small>{issue.description}</small>{issue.remediation && <small className="assessment-remediation">建议：{issue.remediation}</small>}</span> },
  ]
  return <>
    <Row gutter={8} className="assessment-summary">
      <Col span={6}><Card size="small"><Statistic title="得分" value={value.score} suffix="/ 100" /></Card></Col>
      <Col span={6}><Card size="small"><Statistic title="阻断" value={value.blockerCount} styles={{ content: { color: 'var(--color-error)' } }} /></Card></Col>
      <Col span={6}><Card size="small"><Statistic title="警告" value={value.warningCount} styles={{ content: { color: 'var(--color-warning)' } }} /></Card></Col>
      <Col span={6}><Card size="small"><Statistic title="提示" value={value.infoCount} /></Card></Col>
    </Row>
    {value.blockerCount > 0
      ? <Alert showIcon type="error" title={`${value.blockerCount} 项 BLOCKER 禁止继续迁移`} description="解决阻断项并重新发现、评估后才能进入目标映射。" />
      : <Alert showIcon type="success" title="评估门禁通过" description="可以进入目标映射；警告项仍需在 Preflight 前确认。" />}
    <Table<AssessmentIssue> className="assessment-table" size="small" rowKey="id" dataSource={value.issues} columns={columns} pagination={{ pageSize: 8, hideOnSinglePage: true }} locale={{ emptyText: '未发现兼容性问题' }} />
    <div className="assessment-actions"><Button type="primary" disabled={value.blockerCount > 0}>继续目标映射</Button></div>
  </>
}

function severityLabel(value: AssessmentIssue['severity']) {
  return ({ BLOCKER: '阻断', WARNING: '警告', INFO: '提示' })[value]
}

function categoryLabel(value: AssessmentIssue['category']) {
  return ({ COMPUTE: '计算', STORAGE: '存储', NETWORK: '网络', SECURITY: '安全', IMAGE: '镜像', API: 'API', DEPENDENCY: '依赖' })[value]
}

function previewAssessment(applicationID: string): Assessment {
  const id = 'preview-assessment'
  return {
    id, applicationId: applicationID, score: 28, blockerCount: 2, warningCount: 1, infoCount: 1, status: 'COMPLETED', createdAt: '2026-09-03T06:00:00Z', completedAt: '2026-09-03T06:00:00Z',
    issues: [
      { id: 'issue-1', assessmentId: id, severity: 'BLOCKER', category: 'STORAGE', resourceKind: 'PersistentVolumeClaim', resourceNamespace: 'business', resourceName: 'shared-data', ruleId: 'STORAGE_RWX_TARGET', title: '目标集群缺少 RWX 文件存储', description: 'PVC 需要 ReadWriteMany，但目标能力快照中没有 NFS StorageClass。', remediation: '通过外部 NFS 向导安装 NFS CSI 并创建、验证目标 StorageClass。', autoFixable: false },
      { id: 'issue-2', assessmentId: id, severity: 'BLOCKER', category: 'SECURITY', resourceKind: 'Deployment', resourceNamespace: 'business', resourceName: 'api', ruleId: 'SECURITY_HOST_ACCESS', title: '工作负载使用高风险主机能力', description: '检测到 HOST_PATH。', remediation: '移除主机耦合，改用 PVC。', autoFixable: false },
      { id: 'issue-3', assessmentId: id, severity: 'WARNING', category: 'IMAGE', resourceKind: 'Image', resourceName: 'registry.example/api:latest', ruleId: 'IMAGE_MUTABLE_REFERENCE', title: '镜像使用可变 latest 标签', description: '恢复时无法保证内容与源端一致。', remediation: '解析并锁定镜像 digest。', autoFixable: true },
      { id: 'issue-4', assessmentId: id, severity: 'INFO', category: 'COMPUTE', resourceKind: 'Deployment', resourceNamespace: 'business', resourceName: 'api', ruleId: 'COMPUTE_SINGLE_REPLICA', title: '工作负载副本数低于 2', description: '迁移恢复期间没有应用级副本冗余。', autoFixable: false },
    ],
  }
}
