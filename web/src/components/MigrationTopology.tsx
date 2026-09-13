import { useMemo, useState, type ReactNode } from 'react'
import { AimOutlined, CheckCircleFilled, ExclamationCircleFilled, ReloadOutlined, SearchOutlined, SwapRightOutlined } from '@ant-design/icons'
import {
  Background, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type EdgeMouseHandler, type Node, type NodeMouseHandler, type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Alert, Badge, Button, Descriptions, Empty, Input, Segmented, Select, Space, Tabs, Tag, Tooltip, Typography } from 'antd'
import type { MappingChange, ResourceMapping, ResourceMigrationStatus, TopologyEvidence, TopologyNode } from '../api/client'

const { Text } = Typography
const resourceWidth = 278
const resourceHeight = 116
const transformWidth = 238
const transformHeight = 82
const verticalGap = 28
const sourceX = 50
const transformX = 450
const targetX = 810
const nodeTypes = { topologyResource: TopologyResourceNode, topologyTransform: TopologyTransformNode }
const coreKinds = new Set([
  'ComposeService', 'ComposeVolume', 'ComposeNetwork', 'ComposeConfig', 'ComposeSecret',
  'Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'Service', 'Ingress', 'PersistentVolumeClaim', 'ConfigMap', 'Secret',
])
const statusMeta: Record<ResourceMigrationStatus, { color: string; label: string; border: string }> = {
  DISCOVERED: { color: '#64748b', label: '源端发现', border: '#94a3b8' },
  PLANNED: { color: '#64748b', label: '计划迁移', border: '#94a3b8' },
  CREATED: { color: '#1677ff', label: '已创建待验证', border: '#1677ff' },
  SUCCEEDED: { color: '#21a366', label: '迁移成功', border: '#21a366' },
  WARNING: { color: '#d89614', label: '成功有告警', border: '#d89614' },
  FAILED: { color: '#e5484d', label: '创建或验证失败', border: '#e5484d' },
  MISSING: { color: '#e5484d', label: '目标缺失', border: '#e5484d' },
  SKIPPED: { color: '#64748b', label: '无需独立生成', border: '#94a3b8' },
  UNKNOWN: { color: '#64748b', label: '状态未知', border: '#94a3b8' },
}

type Props = {
  evidence?: TopologyEvidence
  loading?: boolean
  refreshing?: boolean
  onRefresh: () => void
}

type Selection =
  | { type: 'resource'; value: TopologyNode }
  | { type: 'mapping'; values: ResourceMapping[] }
  | { type: 'relation'; label: string; source: string; target: string }

export default function MigrationTopology({ evidence, loading, refreshing, onRefresh }: Props) {
  const [selection, setSelection] = useState<Selection>()
  const [focus, setFocus] = useState<{ type: 'node' | 'edge'; id: string }>()
  const [search, setSearch] = useState('')
  const [kind, setKind] = useState<string>()
  const [scope, setScope] = useState<'CORE' | 'ALL'>('CORE')
  const displayedEvidence = useMemo(() => {
    if (!evidence?.currentObservation || evidence.currentObservation.error) return evidence
    const target = evidence.currentObservation.graph
    return { ...evidence, target, mappings: rebindLegacyMappings(evidence.mappings, target.nodes) }
  }, [evidence])
  const baseGraph = useMemo(() => displayedEvidence ? buildFlow(displayedEvidence, search, kind, scope) : { nodes: [], edges: [] }, [displayedEvidence, kind, scope, search])
  const graph = useMemo(() => applyFocus(baseGraph, focus), [baseGraph, focus])
  const nodeIndex = useMemo(() => new Map([...(displayedEvidence?.source.nodes ?? []), ...(displayedEvidence?.target.nodes ?? [])].map((node) => [node.id, node])), [displayedEvidence])
  const mappingIndex = useMemo(() => new Map((displayedEvidence?.mappings ?? []).map((mapping) => [mapping.id, mapping])), [displayedEvidence])
  const kinds = useMemo(() => [...new Set([...(displayedEvidence?.source.nodes ?? []), ...(displayedEvidence?.target.nodes ?? [])].map((node) => node.kind))].sort(), [displayedEvidence])
  const counts = useMemo(() => {
    const result: Partial<Record<ResourceMigrationStatus, number>> = {}
    for (const node of displayedEvidence?.target.nodes ?? []) result[node.status] = (result[node.status] ?? 0) + 1
    return result
  }, [displayedEvidence])
  const blockingCount = useMemo(() => (displayedEvidence?.target.nodes ?? []).filter((node) => node.required && (node.status === 'FAILED' || node.status === 'MISSING')).length, [displayedEvidence])
  const limitations = useMemo(() => {
    const values = evidence?.evidenceLimitations ?? []
    if (!evidence?.currentObservation || evidence.currentObservation.error) return values
    return values.filter((value) => !value.includes('暂时无法检查目标资源') && !value.includes('目标集群当前不可达'))
  }, [evidence])
  const onNodeClick: NodeMouseHandler = (_, node) => {
    setFocus({ type: 'node', id: node.id })
    const resourceID = String(node.data.resourceId ?? '')
    if (resourceID && nodeIndex.has(resourceID)) {
      setSelection({ type: 'resource', value: nodeIndex.get(resourceID)! })
      return
    }
    const mappings = mappingValues(node.data.mappingIds, mappingIndex)
    if (mappings.length) setSelection({ type: 'mapping', values: mappings })
  }
  const onEdgeClick: EdgeMouseHandler = (_, edge) => {
    setFocus({ type: 'edge', id: edge.id })
    const mappings = mappingValues(edge.data?.mappingIds, mappingIndex)
    setSelection(mappings.length ? { type: 'mapping', values: mappings } : { type: 'relation', label: String(edge.data?.relationLabel ?? '资源关系'), source: edge.source, target: edge.target })
  }
  const clearFocus = () => {
    setSelection(undefined)
    setFocus(undefined)
  }

  if (!loading && !evidence) return <Empty description="尚未生成资源拓扑证据" />
  return <div className="migration-topology-panel">
    <div className="topology-toolbar">
      <Space wrap>
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索资源名称" value={search} onChange={(event) => setSearch(event.target.value)} />
        <Select allowClear placeholder="资源类型" value={kind} onChange={setKind} options={kinds.map((value) => ({ value, label: kindLabel(value) }))} />
        <Segmented value={scope} onChange={(value) => setScope(value as 'CORE' | 'ALL')} options={[{ label: '应用视图', value: 'CORE' }, { label: '资源视图', value: 'ALL' }]} />
      </Space>
      <Space>
        {evidence?.currentObservation && <Text type="secondary">当前检查：{formatTime(evidence.currentObservation.checkedAt)} · 漂移 {evidence.currentObservation.drifted}</Text>}
        <Button icon={<ReloadOutlined />} loading={refreshing} onClick={onRefresh}>刷新当前状态</Button>
      </Space>
    </div>
    <div className="topology-summary">
      {(['PLANNED', 'CREATED', 'SUCCEEDED', 'WARNING', 'FAILED', 'MISSING'] as ResourceMigrationStatus[]).map((status) => <span key={status}><Badge color={statusMeta[status].color} />{statusMeta[status].label} <strong>{counts[status] ?? 0}</strong></span>)}
    </div>
    {blockingCount > 0 && <Alert className="topology-panel-notice" showIcon type="error" title="应用级迁移结论：未通过" description="存在失败、缺失或映射未生效的必需资源，不能仅依据任务终态认定迁移成功。" />}
    {evidence?.currentObservation && !evidence.currentObservation.error && <Alert className="topology-panel-notice" showIcon type="info" title="画布显示目标集群当前状态" description="任务执行快照保持不变；资源详情同时展示执行时结论与当前观察，避免已恢复的资源仍被误标为失败。" />}
    {limitations.length ? <Alert className="topology-panel-notice" showIcon type="warning" title={evidence?.snapshotOrigin === 'RECONSTRUCTED' ? '历史证据补建' : '证据提示'} description={[...new Set(limitations)].join('；')} /> : null}
    {evidence?.currentObservation?.error && <Alert className="topology-panel-notice" showIcon type="error" title="目标集群当前状态检查失败" description={evidence.currentObservation.error} />}
    <div className="topology-workspace">
      <div className="topology-canvas">
        <div className="topology-lane-heads">
          <LaneHeader title={`源端 · ${evidence?.source.name ?? '—'}`} subtitle={`${sourceTypeLabel(evidence?.source.type)} / ${evidence?.source.namespace || 'default'}`} />
          <LaneHeader transform title="迁移转换" subtitle="分析 · 转换 · 映射" />
          <LaneHeader target title={`目标端 · ${displayedEvidence?.target.name ?? '—'}`} subtitle={`${sourceTypeLabel(displayedEvidence?.target.type)} / ${displayedEvidence?.target.namespace || 'default'}`} />
        </div>
        <ReactFlow nodeTypes={nodeTypes} nodes={graph.nodes} edges={graph.edges} onNodeClick={onNodeClick} onEdgeClick={onEdgeClick} onPaneClick={clearFocus} defaultViewport={{ x: 10, y: 22, zoom: .78 }} minZoom={.28} maxZoom={1.8} nodesDraggable>
          <Background color="#dce4ed" gap={20} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
        {loading && <div className="topology-loading">正在重建迁移拓扑…</div>}
      </div>
      <TopologyLegend kinds={kinds} />
    </div>
    <div className="topology-focus-hint"><AimOutlined /> 点击资源或迁移线查看证据，相关资源会高亮，其他资源自动置灰</div>
    <section className="topology-detail-region" aria-live="polite">
      {selection && <div className="topology-detail-section-title">{selection.type === 'resource' ? '资源详情' : selection.type === 'mapping' ? '迁移映射详情' : '资源关系详情'}</div>}
      <TopologySelection selection={selection} evidence={displayedEvidence} historicalEvidence={evidence} />
    </section>
  </div>
}

function LaneHeader({ title, subtitle, transform, target }: { title: string; subtitle: string; transform?: boolean; target?: boolean }) {
  return <div className={`topology-lane-header${transform ? ' transform' : ''}${target ? ' target' : ''}`}>
    <span className="topology-lane-icon">{transform ? <SwapRightOutlined /> : target ? 'K8S' : 'SRC'}</span>
    <span><strong>{title}</strong><small>{subtitle}</small></span>
  </div>
}

function TopologyLegend({ kinds }: { kinds: string[] }) {
  const states: ResourceMigrationStatus[] = ['SUCCEEDED', 'WARNING', 'FAILED', 'PLANNED']
  return <aside className="topology-side-legend">
    <strong>图例</strong>
    <div className="legend-line"><i /><span>资源关系</span></div>
    <div className="legend-line mapping"><i /><span>迁移映射</span></div>
    <h4>迁移状态</h4>
    {states.map((status) => <div className="legend-status" key={status}><Badge color={statusMeta[status].color} /><span>{statusMeta[status].label}</span></div>)}
    <h4>资源类型</h4>
    {kinds.filter((value) => coreKinds.has(value)).slice(0, 10).map((value) => <div className="legend-kind" key={value}><span className="kind-glyph">{kindGlyph(value)}</span>{kindLabel(value)}</div>)}
  </aside>
}

function TopologySelection({ selection, evidence, historicalEvidence }: { selection?: Selection; evidence?: TopologyEvidence; historicalEvidence?: TopologyEvidence }) {
  if (!selection) return <div className="topology-detail-empty"><AimOutlined /><span>选择一个资源节点或迁移映射，查看属性、转换差异和验证证据</span></div>
  if (selection.type === 'resource') {
    const node = selection.value
    const historical = node.side === 'TARGET' ? historicalEvidence?.target.nodes.find((item) => item.id === node.id) : undefined
    const related = evidence?.mappings.filter((mapping) => mapping.sourceNodeId === node.id || mapping.targetNodeId === node.id) ?? []
    const changes = [...(node.mappingChanges ?? []), ...related.flatMap((mapping) => mapping.changes ?? [])]
    return <div className="topology-detail-panel">
      <div className="topology-detail-title"><span className="kind-glyph large">{kindGlyph(node.kind)}</span><span><strong>{node.name}</strong><small>{kindLabel(node.kind)} · {node.side === 'SOURCE' ? '源端资源' : '目标资源'}</small></span><Badge color={statusMeta[node.status].color} text={statusMeta[node.status].label} /></div>
      <Descriptions size="small" column={2} items={[
        { key: 'identity', label: '资源标识', children: [node.apiVersion, node.namespace, node.name].filter(Boolean).join(' / ') },
        { key: 'health', label: '当前健康状态', children: node.health || statusMeta[node.status].label },
        { key: 'snapshot', label: '执行时结论', children: historical ? <Badge color={statusMeta[historical.status].color} text={statusMeta[historical.status].label} /> : '—' },
        { key: 'message', label: '当前观察证据', children: node.message || '—' },
        { key: 'snapshotMessage', label: '执行时验证证据', children: historical?.message || '—' },
      ]} />
      {((historical?.status === 'FAILED' || historical?.status === 'MISSING') && historical.message) && <Alert className="topology-resource-error" showIcon type="error" title="执行时资源验证失败" description={historical.message} />}
      {((node.status === 'FAILED' || node.status === 'MISSING') && node.message) && <Alert className="topology-resource-error" showIcon type="error" title="当前资源异常" description={node.message} />}
      <Tabs key={node.id} items={[
        { key: 'attributes', label: '资源属性', children: <AttributeSummary value={node.attributes} /> },
        { key: 'mapping', label: `转换差异（${deduplicateChanges(changes).length}）`, children: <MappingChanges values={deduplicateChanges(changes)} /> },
        { key: 'relations', label: '关联资源', children: <div className="topology-relation-list">{[...(evidence?.source.edges ?? []), ...(evidence?.target.edges ?? [])].filter((edge) => edge.from === node.id || edge.to === node.id).map((edge) => <RelationRow key={edge.id} source={resourceName(evidence, edge.from)} relation={relationLabel(edge.relation)} target={resourceName(evidence, edge.to)} />)}{related.map((mapping) => <RelationRow key={mapping.id} source={resourceName(evidence, mapping.sourceNodeId)} relation="迁移映射" target={resourceName(evidence, mapping.targetNodeId)} mapping />)}</div> },
      ]} />
    </div>
  }
  if (selection.type === 'mapping') {
    const changes = deduplicateChanges(selection.values.flatMap((mapping) => mapping.changes ?? []))
    return <div className="topology-detail-panel">
      <div className="topology-detail-title"><span className="kind-glyph large"><SwapRightOutlined /></span><span><strong>迁移转换与资源映射</strong><small>{selection.values.map((mapping) => mapping.relation).filter(Boolean).join(' · ') || 'MIGRATES_TO'}</small></span><Tag color={changes.some((change) => change.applied === false) ? 'error' : 'success'}>{changes.some((change) => change.applied === false) ? '映射未生效' : '映射已应用'}</Tag></div>
      <div className="topology-detail-grid"><div><h4>资源对应关系</h4>{selection.values.map((mapping) => <div className="mapping-resource-pair" key={mapping.id}><code>{resourceName(evidence, mapping.sourceNodeId)}</code><SwapRightOutlined /><code>{resourceName(evidence, mapping.targetNodeId)}</code></div>)}</div><div><h4>转换差异（Diff）</h4><MappingChanges values={changes} /></div></div>
    </div>
  }
  return <div className="topology-detail-panel"><div className="topology-detail-title"><span className="kind-glyph large">REL</span><span><strong>{selection.label}</strong><small>应用内部资源依赖关系</small></span></div><Descriptions column={1} items={[{ key: 'source', label: '来源资源', children: resourceName(evidence, selection.source) }, { key: 'target', label: '目标资源', children: resourceName(evidence, selection.target) }]} /></div>
}

function RelationRow({ source, relation, target, mapping }: { source: string; relation: string; target: string; mapping?: boolean }) {
  return <div className="topology-relation-row">
    <Text className="topology-relation-resource" ellipsis={{ tooltip: source }}>{source}</Text>
    <span className={`topology-relation-type${mapping ? ' mapping' : ''}`}><span>{relation}</span><SwapRightOutlined /></span>
    <Text className="topology-relation-resource" ellipsis={{ tooltip: target }}>{target}</Text>
  </div>
}

function MappingChanges({ values }: { values: MappingChange[] }) {
  if (!values.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有字段转换" />
  return <div className="mapping-change-list">{values.map((change, index) => <div className={`mapping-change ${change.applied === false ? 'failed' : ''}`} key={`${change.type}-${change.path ?? ''}-${index}`}>
    <span><Tag color="blue">{mappingLabel(change.type)}</Tag>{change.path}</span>
    <code>{change.sourceValue || '—'} → {change.targetValue || '—'}</code>
    {change.applied === true ? <Tooltip title="目标实际值匹配"><CheckCircleFilled className="mapping-applied" /></Tooltip> : change.applied === false ? <Tooltip title="映射未生效"><ExclamationCircleFilled className="mapping-failed" /></Tooltip> : <Tag>待验证</Tag>}
  </div>)}</div>
}

function AttributeSummary({ value }: { value?: Record<string, unknown> }) {
  const entries = Object.entries(value ?? {}).filter(([, item]) => item !== undefined && item !== null)
  if (!entries.length) return <Text type="secondary">暂无可展示属性</Text>
  return <dl className="topology-attribute-summary">{entries.map(([key, item]) => <div key={key}><dt>{attributeLabel(key)}</dt><dd>{formatAttribute(item)}</dd></div>)}</dl>
}

function buildFlow(evidence: TopologyEvidence, search: string, kind: string | undefined, scope: 'CORE' | 'ALL'): { nodes: Node[]; edges: Edge[] } {
  const normalized = search.trim().toLowerCase()
  const source = sortResourcesForApplication(evidence.source.nodes.filter((node) => includeNode(node, normalized, kind, scope)), evidence.source.edges)
  const target = sortResourcesForApplication(evidence.target.nodes.filter((node) => includeNode(node, normalized, kind, scope)), evidence.target.edges)
  const visible = new Set([...source, ...target].map((node) => node.id))
  const targetIndex = new Map(target.map((node) => [node.id, node]))
  const mappingsBySource = new Map<string, ResourceMapping[]>()
  for (const mapping of evidence.mappings) {
    if (!mapping.targetNodeId || !visible.has(mapping.sourceNodeId) || !visible.has(mapping.targetNodeId)) continue
    mappingsBySource.set(mapping.sourceNodeId, [...(mappingsBySource.get(mapping.sourceNodeId) ?? []), mapping])
  }
  const positions = new Map<string, { x: number; y: number }>()
  const transformNodes: Node[] = []
  const mappingEdges: Edge[] = []
  const startY = 142
  const stepY = resourceHeight + verticalGap
  source.forEach((node, index) => positions.set(node.id, { x: sourceX, y: startY + index * stepY }))
  target.forEach((node, index) => positions.set(node.id, { x: targetX, y: startY + index * stepY }))
  let nextTransformY = startY
  for (const sourceNode of source) {
    const mappings = mappingsBySource.get(sourceNode.id) ?? []
    const targetEntries = mappings.map((mapping) => [mapping.targetNodeId!, targetIndex.get(mapping.targetNodeId!)] as const).filter((entry): entry is readonly [string, TopologyNode] => Boolean(entry[1]))
    const targets = sortResources([...new Map(targetEntries).values()])
    if (mappings.length) {
      const transformID = `transform:${sourceNode.id}`
      const failed = mappings.some((mapping) => mapping.changes?.some((change) => change.applied === false))
      const sourceY = positions.get(sourceNode.id)!.y
      const mappedY = targets.map((item) => positions.get(item.id)?.y).filter((value): value is number => value !== undefined)
      const preferredY = mappedY.length ? (sourceY + mappedY.reduce((sum, value) => sum + value, 0) / mappedY.length) / 2 : sourceY
      const transformY = Math.max(nextTransformY, preferredY)
      transformNodes.push(buildTransformNode(transformID, sourceNode, targets, mappings, { x: transformX, y: transformY }, failed))
      mappingEdges.push(mappingEdge(`mapping-in:${sourceNode.id}`, sourceNode.id, transformID, 'right', 'left', mappings, failed))
      targets.forEach((targetNode) => {
        const targetMappings = mappings.filter((mapping) => mapping.targetNodeId === targetNode.id)
        const targetFailed = targetMappings.some((mapping) => mapping.changes?.some((change) => change.applied === false))
        mappingEdges.push(mappingEdge(`mapping-out:${sourceNode.id}:${targetNode.id}`, transformID, targetNode.id, 'right', 'left', targetMappings, targetFailed))
      })
      nextTransformY = transformY + transformHeight + verticalGap
    }
  }
  const resourceNodes = [...source, ...target].map((node) => buildResourceNode(node, positions.get(node.id) ?? { x: node.side === 'SOURCE' ? sourceX : targetX, y: startY }))
  const topologyEdges = [...evidence.source.edges, ...evidence.target.edges].filter((edge) => visible.has(edge.from) && visible.has(edge.to)).map<Edge>((edge) => {
    const failed = edge.status === 'FAILED' || edge.status === 'MISSING'
    return {
      id: edge.id, source: edge.from, target: edge.to, sourceHandle: 'bottom', targetHandle: 'top', type: 'smoothstep', interactionWidth: 22,
      data: { relationLabel: relationLabel(edge.relation), mapping: false },
      style: { stroke: failed ? '#e5484d' : '#9aabba', strokeWidth: 1.45 }, markerEnd: { type: MarkerType.ArrowClosed, color: failed ? '#e5484d' : '#9aabba' },
    }
  })
  return { nodes: [...resourceNodes, ...transformNodes], edges: [...topologyEdges, ...mappingEdges] }
}

function buildResourceNode(value: TopologyNode, position: { x: number; y: number }): Node {
  const status = statusMeta[value.status]
  const mapped = value.mappingChanges?.some((change) => change.changed)
  const failedMapping = value.mappingChanges?.some((change) => change.applied === false)
  const failed = value.status === 'FAILED' || value.status === 'MISSING'
  return {
    id: value.id, type: 'topologyResource', position,
    data: { resourceId: value.id, label: <div className="topology-node-label"><div className="topology-node-title"><span className="kind-glyph">{kindGlyph(value.kind)}</span><span><strong title={value.name}>{value.name}</strong><small>{kindLabel(value.kind)}</small></span><Badge color={status.color} /></div><div className="topology-node-facts"><span title={nodeFact(value)}>{nodeFact(value)}</span>{value.health && <span>{value.health}</span>}</div>{failed && value.message && <Tooltip title={value.message}><div className="topology-node-error"><ExclamationCircleFilled /> {value.message}</div></Tooltip>}<div className="topology-node-footer"><small>{value.namespace || value.apiVersion || '—'}</small>{mapped && <Tag color={failedMapping ? 'error' : 'blue'}>{failedMapping ? '映射未生效' : '已映射'}</Tag>}</div></div> },
    style: { width: resourceWidth, minHeight: resourceHeight, border: `1px ${value.status === 'PLANNED' ? 'dashed' : 'solid'} ${status.border}`, borderRadius: 8, background: '#fff', boxShadow: '0 2px 8px rgba(15,35,55,.08)', padding: 11 },
  }
}

function buildTransformNode(id: string, source: TopologyNode, targets: TopologyNode[], mappings: ResourceMapping[], position: { x: number; y: number }, failed: boolean): Node {
  const changed = deduplicateChanges(mappings.flatMap((mapping) => mapping.changes ?? []))
  const targetKinds = targets.map((target) => kindLabel(target.kind)).filter((value, index, values) => values.indexOf(value) === index)
  const changeLabels = changed.map((change) => mappingLabel(change.type)).filter((value, index, values) => values.indexOf(value) === index)
  return {
    id, type: 'topologyTransform', position,
    data: { mappingIds: mappings.map((mapping) => mapping.id), label: <div className="topology-transform-label"><div><span className="kind-glyph"><SwapRightOutlined /></span><span><strong>{kindLabel(source.kind)}</strong><small>→ {targetKinds.join(' + ') || '目标资源'}</small></span></div><p><CheckCircleFilled /> {failed ? '映射未生效' : changeLabels.join(' · ') || '自动转换'}</p></div> },
    style: { width: transformWidth, minHeight: transformHeight, border: `1px solid ${failed ? '#e5484d' : '#91caff'}`, borderRadius: 8, background: failed ? '#fff7f7' : '#f4f9ff', boxShadow: '0 2px 8px rgba(22,119,255,.08)', padding: 11 },
  }
}

function mappingEdge(id: string, source: string, target: string, sourceHandle: string, targetHandle: string, mappings: ResourceMapping[], failed: boolean): Edge {
  return { id, source, target, sourceHandle, targetHandle, type: 'smoothstep', interactionWidth: 26, data: { relationLabel: failed ? '映射未生效' : '迁移映射', mapping: true, mappingIds: mappings.map((mapping) => mapping.id) }, style: { stroke: failed ? '#e5484d' : '#3f8df5', strokeWidth: 1.8, strokeDasharray: failed ? undefined : '7 5' }, markerEnd: { type: MarkerType.ArrowClosed, color: failed ? '#e5484d' : '#1677ff' } }
}

function TopologyResourceNode({ data }: NodeProps) {
  return <><Handle id="top" type="target" position={Position.Top} className="topology-node-handle" /><Handle id="bottom" type="source" position={Position.Bottom} className="topology-node-handle" /><Handle id="left" type="target" position={Position.Left} className="topology-node-handle topology-node-handle-side" /><Handle id="right" type="source" position={Position.Right} className="topology-node-handle topology-node-handle-side" />{data.label as ReactNode}</>
}

function TopologyTransformNode({ data }: NodeProps) {
  return <><Handle id="left" type="target" position={Position.Left} className="topology-node-handle topology-node-handle-side" /><Handle id="right" type="source" position={Position.Right} className="topology-node-handle topology-node-handle-side" />{data.label as ReactNode}</>
}

function includeNode(node: TopologyNode, search: string, kind: string | undefined, scope: 'CORE' | 'ALL') {
  if (kind && node.kind !== kind) return false
  if (scope === 'CORE' && !coreKinds.has(node.kind)) return false
  return !search || `${node.kind} ${node.namespace ?? ''} ${node.name}`.toLowerCase().includes(search)
}

function sortResources(values: TopologyNode[]) { return [...values].sort((left, right) => resourceRank(left.kind) - resourceRank(right.kind) || left.name.localeCompare(right.name)) }
function sortResourcesForApplication(values: TopologyNode[], edges: TopologyEvidence['source']['edges']) {
  const nodeIndex = new Map(values.map((node) => [node.id, node]))
  const group = new Map(values.map((node) => [node.id, node.name]))
  for (let pass = 0; pass < 3; pass++) {
    for (const edge of edges) {
      const from = nodeIndex.get(edge.from)
      const to = nodeIndex.get(edge.to)
      if (!from || !to) continue
      if (['ROUTES_TO', 'SELECTS'].includes(edge.relation)) {
        const name = from.kind === 'Ingress' ? to.name : from.name
        group.set(from.id, name)
        group.set(to.id, name)
      } else if (['MOUNTS', 'CONFIGURES'].includes(edge.relation)) {
        group.set(to.id, group.get(from.id) ?? from.name)
      }
    }
  }
  return [...values].sort((left, right) => {
    const leftGroup = group.get(left.id) ?? left.name
    const rightGroup = group.get(right.id) ?? right.name
    return leftGroup.localeCompare(rightGroup) || resourceRank(left.kind) - resourceRank(right.kind) || left.name.localeCompare(right.name)
  })
}
function resourceRank(kind: string) {
  if (kind === 'Ingress') return 0
  if (kind === 'Service') return 1
  if (['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'ComposeService'].includes(kind)) return 2
  if (['PersistentVolumeClaim', 'ComposeVolume'].includes(kind)) return 3
  if (['ConfigMap', 'Secret', 'ComposeConfig', 'ComposeSecret'].includes(kind)) return 4
  if (kind === 'ComposeNetwork') return 5
  return 6
}

function applyFocus(graph: { nodes: Node[]; edges: Edge[] }, focus?: { type: 'node' | 'edge'; id: string }) {
  if (!focus) return graph
  const edgeIndex = new Map(graph.edges.map((edge) => [edge.id, edge]))
  const nodeIDs = new Set(graph.nodes.map((node) => node.id))
  if (focus.type === 'node' && !nodeIDs.has(focus.id)) return graph
  if (focus.type === 'edge' && !edgeIndex.has(focus.id)) return graph
  const adjacency = new Map<string, Array<{ node: string; edge: string }>>()
  for (const edge of graph.edges) {
    const from = adjacency.get(edge.source) ?? []
    const to = adjacency.get(edge.target) ?? []
    from.push({ node: edge.target, edge: edge.id }); to.push({ node: edge.source, edge: edge.id })
    adjacency.set(edge.source, from); adjacency.set(edge.target, to)
  }
  const relatedNodes = new Set<string>()
  const relatedEdges = new Set<string>()
  if (focus.type === 'node') {
    relatedNodes.add(focus.id)
    for (const relation of adjacency.get(focus.id) ?? []) {
      relatedNodes.add(relation.node)
      relatedEdges.add(relation.edge)
    }
  } else {
    const edge = edgeIndex.get(focus.id)!
    relatedEdges.add(edge.id); relatedNodes.add(edge.source); relatedNodes.add(edge.target)
  }
  for (const nodeID of [...relatedNodes]) {
    if (!nodeID.startsWith('transform:')) continue
    for (const relation of adjacency.get(nodeID) ?? []) {
      const edge = edgeIndex.get(relation.edge)
      if (edge?.data?.mapping !== true) continue
      relatedEdges.add(relation.edge)
      relatedNodes.add(relation.node)
    }
  }
  return {
    nodes: graph.nodes.map((node) => {
      const active = relatedNodes.has(node.id)
      const selectedNode = focus.type === 'node' && focus.id === node.id
      return { ...node, zIndex: active ? 2 : 0, style: { ...node.style, opacity: active ? 1 : .14, filter: active ? 'none' : 'grayscale(1)', boxShadow: selectedNode ? '0 0 0 3px rgba(22,119,255,.24), 0 8px 20px rgba(15,35,55,.18)' : node.style?.boxShadow, transition: 'opacity .18s ease, filter .18s ease, box-shadow .18s ease' } }
    }),
    edges: graph.edges.map((edge) => {
      const active = relatedEdges.has(edge.id)
      const selectedEdge = focus.type === 'edge' && focus.id === edge.id
      const baseWidth = Number(edge.style?.strokeWidth ?? 1.5)
      return { ...edge, label: active ? String(edge.data?.relationLabel ?? '') : undefined, animated: selectedEdge && edge.data?.mapping === true, zIndex: selectedEdge ? 3 : active ? 1 : 0, style: { ...edge.style, opacity: active ? 1 : .06, strokeWidth: selectedEdge ? baseWidth + 2 : active ? baseWidth + .5 : baseWidth, transition: 'opacity .18s ease, stroke-width .18s ease' } }
    }),
  }
}

function mappingValues(value: unknown, index: Map<string, ResourceMapping>) { return (Array.isArray(value) ? value : []).map((id) => index.get(String(id))).filter((item): item is ResourceMapping => Boolean(item)) }
function rebindLegacyMappings(mappings: ResourceMapping[], targetNodes: TopologyNode[]) {
  const targetIDs = new Set(targetNodes.map((node) => node.id))
  const targetByIdentity = new Map(targetNodes.map((node) => [`${node.kind}|${node.namespace}|${kubernetesName(node.name)}`, node.id]))
  return mappings.map((mapping) => {
    if (!mapping.targetNodeId || targetIDs.has(mapping.targetNodeId)) return mapping
    const parts = mapping.targetNodeId.split('|')
    if (parts.length < 5) return mapping
    const targetNodeId = targetByIdentity.get(`${parts[2]}|${parts[3]}|${kubernetesName(parts.slice(4).join('|'))}`)
    return targetNodeId ? { ...mapping, targetNodeId } : mapping
  })
}
function kubernetesName(value: string) {
  return value.toLowerCase().trim().replace(/[^a-z0-9.-]+/g, '-').replace(/-+/g, '-').replace(/^[.-]+|[.-]+$/g, '').slice(0, 63).replace(/[.-]+$/g, '') || 'resource'
}
function resourceName(evidence: TopologyEvidence | undefined, id?: string) {
  if (!id) return '无需独立生成'
  const node = [...(evidence?.source.nodes ?? []), ...(evidence?.target.nodes ?? [])].find((item) => item.id === id)
  return node ? `${kindLabel(node.kind)} / ${node.name}` : id
}
function deduplicateChanges(values: MappingChange[]) { return [...new Map(values.map((value) => [`${value.type}\u0000${value.path ?? ''}\u0000${value.sourceValue}\u0000${value.targetValue}`, value])).values()] }
function nodeFact(node: TopologyNode) {
  const attributes = node.attributes ?? {}
  const image = attributes.image ?? (Array.isArray(attributes.images) ? attributes.images[0] : undefined)
  if (image) return `image: ${String(image)}`
  const storageClass = attributes.storageClass ?? attributes.storageClassName
  if (storageClass) return `StorageClass: ${String(storageClass)}`
  const replicas = attributes.replicas ?? attributes.readyReplicas
  if (replicas !== undefined) return `replicas: ${String(replicas)}`
  return node.message || node.apiVersion || '资源已纳入迁移清单'
}
function kindGlyph(kind: string) {
  const values: Record<string, string> = { Ingress: 'ING', Service: 'SVC', Deployment: 'DEP', StatefulSet: 'STS', DaemonSet: 'DS', PersistentVolumeClaim: 'PVC', ConfigMap: 'CM', Secret: 'SEC', ComposeService: 'APP', ComposeVolume: 'VOL', ComposeNetwork: 'NET' }
  const abbreviation = kind.replace(/[^A-Z]/g, '').slice(0, 3)
  return values[kind] ?? (abbreviation || kind.slice(0, 3).toUpperCase())
}
function kindLabel(kind: string) {
  const values: Record<string, string> = { ComposeService: 'Compose Service', ComposeVolume: 'Compose Volume', ComposeNetwork: 'Compose Network', ComposeConfig: 'Compose Config', ComposeSecret: 'Compose Secret', PersistentVolumeClaim: 'PersistentVolumeClaim' }
  return values[kind] ?? kind
}
function mappingLabel(type: string) {
  const values: Record<string, string> = { IMAGE: '镜像映射', REGISTRY: '镜像仓库', STORAGE_CLASS: '存储映射', NAMESPACE: 'Namespace', INGRESS_CLASS: 'IngressClass', NODE_LABEL: 'NodeLabel', NFS: 'NFS 映射', API_VERSION: 'API 版本', RESOURCE_NAME: '资源名称' }
  return values[type] ?? type
}
function relationLabel(value: string) {
  const values: Record<string, string> = { SELECTS: '选择工作负载', MOUNTS: '挂载卷', CONFIGURES: '引用配置', ROUTES_TO: '路由至服务', DEPENDS_ON: '依赖', ATTACHED_TO: '连接网络' }
  return values[value] ?? value
}
function sourceTypeLabel(value?: string) { return value === 'COMPOSE' ? 'DOCKER COMPOSE' : value || 'KUBERNETES' }
function attributeLabel(value: string) { return value.replace(/([A-Z])/g, ' $1').replace(/^./, (char) => char.toUpperCase()) }
function formatAttribute(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value) }
function formatTime(value: string) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value)) }
