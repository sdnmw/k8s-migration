import { useMemo, useState } from 'react'
import { CheckCircleOutlined, FileTextOutlined, SwapOutlined, UploadOutlined } from '@ant-design/icons'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Empty, Select, Space, Statistic, Tabs, Tag, Typography, Upload } from 'antd'
import {
  listMappingProfiles, previewManifestTransform,
  type MappingProfile, type TransformDocument, type TransformResult,
} from '../api/client'
import PageHeader from '../components/PageHeader'

const { Text } = Typography

export default function TransformPreviewPage({ preview = false }: { preview?: boolean }) {
  const [profileId, setProfileId] = useState(preview ? 'preview-profile' : '')
  const [manifest, setManifest] = useState<File>()
  const [result, setResult] = useState<TransformResult | undefined>(preview ? previewResult : undefined)
  const [selectedKey, setSelectedKey] = useState(preview ? documentKey(previewResult.documents[0]) : '')
  const profiles = useQuery({
    queryKey: ['mapping-profiles'], queryFn: () => listMappingProfiles(), enabled: !preview,
    initialData: preview ? previewProfiles : undefined,
  })
  const run = useMutation({
    mutationFn: () => preview ? Promise.resolve(previewResult) : previewManifestTransform(profileId, manifest!),
    onSuccess: (value) => { setResult(value); setSelectedKey(value.documents[0] ? documentKey(value.documents[0]) : '') },
  })
  const selected = useMemo(
    () => result?.documents.find((item) => documentKey(item) === selectedKey) ?? result?.documents[0],
    [result, selectedKey],
  )

  return <>
    <PageHeader title="资源转换规则" description="将源端 YAML 转换为目标 SKS 可部署资源，并在部署前检查最终差异。" />
    <Alert className="page-alert" showIcon type="info" title="预览会清理运行时字段、转换旧 API，并应用当前映射配置。Secret 内容在源、目标和 Diff 中始终脱敏。" />
    <Card className="section-card transform-input-card" variant="outlined">
      <div className="transform-input-grid">
        <div><Text className="field-label">映射配置</Text><Select value={profileId || undefined} onChange={setProfileId} loading={profiles.isPending} placeholder="选择目标映射" options={(profiles.data ?? []).map((item) => ({ value: item.id, label: `${item.name} · ${item.targetEnvironmentId}` }))} /></div>
        <div><Text className="field-label">源端 Kubernetes YAML</Text><Space.Compact block><Upload accept=".yaml,.yml" maxCount={1} showUploadList={false} beforeUpload={(file) => { setManifest(file); setResult(undefined); return false }}><Button icon={<UploadOutlined />}>选择 YAML</Button></Upload><div className="transform-file-name">{manifest?.name ?? (preview ? 'production-resources.yaml' : '尚未选择文件')}</div></Space.Compact></div>
        <Button type="primary" icon={<SwapOutlined />} loading={run.isPending} disabled={!profileId || (!manifest && !preview)} onClick={() => run.mutate()}>生成转换预览</Button>
      </div>
      {run.isError && <Alert className="transform-error" showIcon type="error" title="转换失败" description={run.error instanceof Error ? run.error.message : '请求失败'} />}
    </Card>
    {result ? <TransformResultPanel result={result} selected={selected} selectedKey={selectedKey} onSelect={setSelectedKey} /> : <Card className="section-card transform-empty-card" variant="outlined"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择映射配置并上传 YAML 后生成预览" /></Card>}
  </>
}

function TransformResultPanel({ result, selected, selectedKey, onSelect }: { result: TransformResult; selected?: TransformDocument; selectedKey: string; onSelect: (value: string) => void }) {
  return <div className="transform-result-grid">
    <Card className="section-card transform-resource-card" title="资源清单" extra={<Tag color="blue">{result.documents.length} 个对象</Tag>} variant="outlined">
      <div className="transform-statistics">
        <Statistic title="已转换" value={result.changed} suffix={`/ ${result.documents.length}`} prefix={<CheckCircleOutlined />} />
      </div>
      <div className="transform-resource-list">{result.documents.map((item) => {
        const key = documentKey(item)
        return <button type="button" className={key === selectedKey ? 'active' : ''} key={key} onClick={() => onSelect(key)}>
          <FileTextOutlined /><span><strong>{item.kind} / {item.name}</strong><small>{item.namespace || '集群级'} · {item.apiVersion}</small></span>{item.changed && <Tag color="processing">已变更</Tag>}
        </button>
      })}</div>
    </Card>
    <Card className="section-card transform-code-card" title={selected ? `${selected.kind} / ${selected.name}` : '转换详情'} variant="outlined">
      {selected ? <Tabs items={[
        { key: 'target', label: '目标 YAML', children: <ManifestCode value={selected.targetYaml} /> },
        { key: 'diff', label: 'Unified Diff', children: <ManifestCode value={selected.unifiedDiff || '无差异'} diff /> },
        { key: 'source', label: '源 YAML', children: <ManifestCode value={selected.sourceYaml} /> },
      ]} /> : <Empty description="没有可显示的资源" />}
    </Card>
  </div>
}

function ManifestCode({ value, diff = false }: { value: string; diff?: boolean }) {
  return <pre className={diff ? 'manifest-code manifest-diff' : 'manifest-code'}>{value}</pre>
}

function documentKey(value: TransformDocument) { return `${value.kind}/${value.namespace ?? ''}/${value.name}/${value.apiVersion}` }

const previewProfiles: MappingProfile[] = [{ id: 'preview-profile', name: '生产默认映射', targetEnvironmentId: 'SKS-Production', storageMappings: [], namespaceMappings: [], ingressMappings: [], registryMappings: [], nfsMappings: [], nodeLabelMappings: [], createdAt: '2026-09-03T05:30:00Z', updatedAt: '2026-09-03T06:30:00Z' }]
const previewResult: TransformResult = { changed: 3, documents: [
  { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'business-prod', name: 'api', changed: true, sourceYaml: 'apiVersion: apps/v1beta1\nkind: Deployment\nmetadata:\n  name: api\n  namespace: business\n', targetYaml: 'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n  namespace: business-prod\nspec:\n  template:\n    spec:\n      containers:\n      - image: harbor.example.local/migrated/team/api:v1\n', unifiedDiff: '--- source\n+++ target\n@@ -1,5 +1,10 @@\n-apiVersion: apps/v1beta1\n+apiVersion: apps/v1\n kind: Deployment\n metadata:\n   name: api\n-  namespace: business\n+  namespace: business-prod\n+spec:\n+  template:\n+    spec:\n+      containers:\n+      - image: harbor.example.local/migrated/team/api:v1\n' },
  { apiVersion: 'networking.k8s.io/v1', kind: 'Ingress', namespace: 'business-prod', name: 'api', changed: true, sourceYaml: 'apiVersion: extensions/v1beta1\nkind: Ingress\nmetadata:\n  name: api\n', targetYaml: 'apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: api\n  namespace: business-prod\n', unifiedDiff: '--- source\n+++ target\n@@ -1,4 +1,5 @@\n-apiVersion: extensions/v1beta1\n+apiVersion: networking.k8s.io/v1\n kind: Ingress\n metadata:\n   name: api\n+  namespace: business-prod\n' },
  { apiVersion: 'v1', kind: 'Secret', namespace: 'business-prod', name: 'database', changed: true, sourceYaml: 'apiVersion: v1\ndata:\n  password: <redacted>\nkind: Secret\nmetadata:\n  name: database\n  namespace: business\n', targetYaml: 'apiVersion: v1\ndata:\n  password: <redacted>\nkind: Secret\nmetadata:\n  name: database\n  namespace: business-prod\n', unifiedDiff: '--- source\n+++ target\n@@ -5,4 +5,4 @@\n metadata:\n   name: database\n-  namespace: business\n+  namespace: business-prod\n' },
] }
