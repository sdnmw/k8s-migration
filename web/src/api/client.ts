export type Administrator = {
  id: string
  username: string
}

export type EnvironmentRole = 'SOURCE' | 'TARGET'
export type EnvironmentStatus = 'PENDING' | 'CONNECTED' | 'DISCONNECTED' | 'ERROR'

export type ClusterCapabilities = {
  kubernetesVersion?: string
  architectures?: string[]
  operatingSystems?: string[]
  nodeCount?: number
  namespaceCount?: number
  storageClasses?: Array<{ name: string; provisioner: string; default: boolean; allowExpansion: boolean; volumeBindingMode?: string }>
  csiDrivers?: string[]
  volumeSnapshotClasses?: string[]
  volumeSnapshotClassDetails?: Array<{ name: string; driver: string; deletionPolicy?: string }>
  csiDataMover?: {
    snapshotApi: boolean; dataUploadApi: boolean; dataDownloadApi: boolean; backupRepositoryApi: boolean
    enableCsi: boolean; nodeAgentDesired: number; nodeAgentReady: number; backupReady: boolean; restoreReady: boolean
  }
  ingressClasses?: string[]
  apiGroups?: string[]
  allocatable?: Record<string, string>
  security?: Record<string, unknown>
  runtime?: Record<string, string>
}

export type Environment = {
  id: string
  name: string
  role: EnvironmentRole
  kind: 'KUBERNETES' | 'DOCKER_COMPOSE'
  endpoint?: string
  status: EnvironmentStatus
  statusMessage?: string
  capabilities: ClusterCapabilities
  capabilitiesUpdatedAt?: string | null
  createdAt: string
  updatedAt: string
}

export type StorageProfileType = 'SMTX_BLOCK' | 'EXISTING_NFS_SC' | 'EXTERNAL_NFS_SC'
export type StorageProfileStatus = 'PENDING' | 'INSTALLING' | 'READY' | 'FAILED'
export type StorageProfileInput = {
  environmentId: string
  name: string
  type: StorageProfileType
  storageClassName: string
  nfsServer?: string
  nfsExport?: string
  mountOptions?: string[]
  reclaimPolicy: 'Retain' | 'Delete'
}
export type StorageProfile = StorageProfileInput & {
  id: string
  provisioner?: string
  status: StorageProfileStatus
  createdAt: string
  updatedAt: string
}
export type StorageProfileTestResult = {
  profile: StorageProfile
  probe: { storageClass: string; pvcName: string; bytes: number; remounted: boolean }
  checks: Array<{ name: string; status: 'PASSED' | 'FAILED' | 'WARNING'; message: string }>
}

export type ObjectStorageProfile = {
  id: string; name: string; endpoint: string; bucket: string; region: string; tlsVerify: boolean
  createdAt: string; updatedAt: string
}
export type AddonInstallation = {
  id: string; environmentId: string; type: 'MINIO' | 'VELERO' | 'NFS_CSI'; version: string
  status: 'PENDING' | 'INSTALLING' | 'READY' | 'FAILED' | 'REMOVED'; message?: string; values?: Record<string, unknown>
  createdAt: string; updatedAt: string
}
export type MinIOSourcePolicy = {
  officialImage: { repository: string; tag: string; digest: string }
  releaseAllowed: boolean
}
export type VeleroInstallResult = {
  installation: AddonInstallation
  backupStorageLocation: { name: string; phase: string; message?: string; lastValidationTime?: string }
  checks: Array<{ name: string; status: 'PASSED' | 'FAILED' | 'WARNING'; message: string }>
}

export type MinIOBootstrapInput = {
  environmentId: string; name?: string; endpoint: string; bucket?: string; region?: string
  storageClass: string; storageSize?: string; imageRepository: string; imageDigest: string
  tlsSecretName: string; caBundle?: string
}
export type MinIOAdoptInput = Pick<MinIOBootstrapInput, 'environmentId' | 'name' | 'endpoint' | 'bucket' | 'region' | 'tlsSecretName' | 'caBundle'>
export type ExternalS3Input = {
  name: string; endpoint: string; bucket: string; region?: string
  accessKey: string; secretKey: string; caBundle?: string
}

export function listObjectStorageProfiles() {
  return request<ObjectStorageProfile[]>('/object-storage/profiles')
}

export function getMinIOSourcePolicy() {
  return request<MinIOSourcePolicy>('/object-storage/minio/source-policy')
}

export function bootstrapMinIO(input: MinIOBootstrapInput) {
  return request<{ installation: AddonInstallation; profile: ObjectStorageProfile; checks: ConnectionTest['checks'] }>('/object-storage/bootstrap', {
    method: 'POST', body: JSON.stringify(input),
  })
}

export function adoptManagedMinIO(input: MinIOAdoptInput) {
  return request<{ installation: AddonInstallation; profile: ObjectStorageProfile; checks: ConnectionTest['checks'] }>('/object-storage/minio/adopt', {
    method: 'POST', body: JSON.stringify(input),
  })
}

export function connectExternalS3(input: ExternalS3Input) {
  return request<{ profile: ObjectStorageProfile; checks: ConnectionTest['checks'] }>('/object-storage/profiles', {
    method: 'POST', body: JSON.stringify(input),
  })
}

export function testMinIO(profileId: string) {
  return request<ConnectionTest>('/object-storage/minio/test', { method: 'POST', body: JSON.stringify({ profileId }) })
}

export function listAddonStatuses(environmentId: string) {
  return request<AddonInstallation[]>(`/addons/${encodeURIComponent(environmentId)}/status`)
}

export function installVelero(environmentId: string, objectStorageProfileId: string, kubeletRoot = '/var/lib/kubelet', prefix = 'migrations') {
  return request<VeleroInstallResult>(`/addons/${encodeURIComponent(environmentId)}/install`, {
    method: 'POST', body: JSON.stringify({ type: 'VELERO', objectStorageProfileId, kubeletRoot, prefix }),
  })
}

export function reuseVelero(environmentId: string, objectStorageProfileId: string, prefix = 'migrations') {
  return request<VeleroInstallResult>(`/addons/${encodeURIComponent(environmentId)}/velero/reuse`, {
    method: 'POST', body: JSON.stringify({ objectStorageProfileId, prefix }),
  })
}

export function uninstallVelero(environmentId: string) {
  return request<AddonInstallation>(`/addons/${encodeURIComponent(environmentId)}/velero`, { method: 'DELETE' })
}

export type ConnectionTest = {
  success: boolean
  checks: Array<{ name: string; status: 'PASSED' | 'FAILED' | 'WARNING'; message: string }>
}

export class APIError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message)
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body && !(options.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(options.method ?? 'GET')) {
    const csrf = readCookie('sks_migration_csrf')
    if (csrf) headers.set('X-CSRF-Token', csrf)
  }
  let response: Response
  try {
    response = await fetch(`/api/v1${path}`, { ...options, headers, credentials: 'same-origin' })
  } catch (error) {
    throw new APIError(0, 'NETWORK_ERROR', `无法连接迁移服务：${error instanceof Error ? error.message : '网络请求失败'}`)
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({})) as { code?: string; detail?: string }
    const fallback = response.status === 504
      ? '应用扫描超过网关等待时间。系统已保留已有应用，请稍后重试或按 Namespace 手动选择资源。'
      : response.status === 502
        ? '迁移 API 暂时不可用，请检查服务状态后重试。'
        : `请求失败（HTTP ${response.status}）`
    throw new APIError(response.status, body.code ?? `HTTP_${response.status}`, body.detail ?? fallback)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export function getCurrentAdministrator() {
  return request<Administrator>('/auth/me')
}

export function login(username: string, password: string) {
  return request<void>('/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

export function logout() {
  return request<void>('/auth/logout', { method: 'POST' })
}

export function changePassword(currentPassword: string, newPassword: string) {
  return request<void>('/auth/password', { method: 'POST', body: JSON.stringify({ currentPassword, newPassword }) })
}

export function listEnvironments() {
  return request<Environment[]>('/environments')
}

export function listStorageProfiles(environmentId?: string) {
  const query = environmentId ? `?environmentId=${encodeURIComponent(environmentId)}` : ''
  return request<StorageProfile[]>(`/storage-profiles${query}`)
}

export function createStorageProfile(input: StorageProfileInput) {
  return request<StorageProfile>('/storage-profiles', { method: 'POST', body: JSON.stringify(input) })
}

export function installStorageProfile(id: string) {
  return request<StorageProfileTestResult>(`/storage-profiles/${id}/install`, { method: 'POST' })
}

export function testStorageProfile(id: string) {
  return request<StorageProfileTestResult>(`/storage-profiles/${id}/test`, { method: 'POST' })
}

export type CreateEnvironmentInput =
  | { name: string; role: EnvironmentRole; kind: 'KUBERNETES'; credential: string }
  | { name: string; role: 'SOURCE'; kind: 'DOCKER_COMPOSE'; endpoint: string; ssh: { username: string; password?: string; privateKey?: string; privateKeyPassword?: string; hostKeyFingerprint: string } }

export function createEnvironment(input: CreateEnvironmentInput) {
  return request<Environment>('/environments', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export type ComposeInventory = {
  projectName: string
	status?: string
	configFiles?: string[]
	workingDir?: string
  services: Array<{
    name: string; image?: string; build: boolean; profiles: string[]; dependsOn: string[]
    ports: Array<{ target: number; published?: string; protocol: string; hostIp?: string }>
    mounts: Array<{ type: string; source?: string; target: string; readOnly: boolean }>
    networks: string[]; environmentKeys: string[]; privileged: boolean; networkMode?: string
  }>
  volumes: Array<{ name: string; runtimeName?: string; driver?: string; external: boolean }>
  networks: Array<{ name: string; runtimeName?: string; driver?: string; external: boolean }>
  configs: string[]
  secrets: string[]
  disabledServices: string[]
  warnings: Array<{ code: string; service?: string; message: string }>
}

export type ResourceReference = { apiVersion?: string; kind: string; namespace?: string; name: string }
export type ResourceSummary = ResourceReference & {
  labels?: Record<string, string>; images?: string[]; dataKeys?: string[]; secretKeys?: string[]; replicas?: number
  requests?: Record<string, string>; limits?: Record<string, string>; missingRequests?: Array<'cpu' | 'memory'>
  ingressClassName?: string; serviceType?: string; nodeSelectors?: Record<string, string>; nfsSources?: string[]; securityRisks?: string[]
}
export type KubernetesInventory = {
  resources: ResourceSummary[]; workloads: ResourceSummary[]; services: ResourceSummary[]; ingresses: ResourceSummary[]
  configMaps: ResourceSummary[]; secrets: ResourceSummary[]; serviceAccounts: ResourceSummary[]; roles: ResourceSummary[]
  roleBindings: ResourceSummary[]; crds: ResourceSummary[]; customResources: ResourceSummary[]
  pvcs: Array<{ name: string; namespace?: string; capacityBytes?: number; storageClassName?: string; accessModes?: string[]; volumeMode?: string }>
  images: Array<{ reference: string; digest?: string; architectures?: string[] }>
  dependencies: Array<{ from: ResourceReference; to: ResourceReference; type: string; required: boolean }>
  warnings: Array<{ code: string; resource?: string; message: string }>
  counts: Record<string, number>
  compose?: ComposeInventory
}

export type SourceApplication = {
  id: string; environmentId: string; name: string; sourceType: 'KUBERNETES' | 'COMPOSE'; namespace: string
  inventory: KubernetesInventory; createdAt: string; updatedAt: string
}

export type AssessmentIssue = {
  id: string; assessmentId: string; severity: 'BLOCKER' | 'WARNING' | 'INFO'
  category: 'COMPUTE' | 'STORAGE' | 'NETWORK' | 'SECURITY' | 'IMAGE' | 'API' | 'DEPENDENCY'
  resourceKind: string; resourceNamespace?: string; resourceName: string; ruleId: string
  title: string; description: string; remediation?: string; autoFixable: boolean
}

export type Assessment = {
  id: string; applicationId: string; score: number; blockerCount: number; warningCount: number; infoCount: number
  status: 'PENDING' | 'RUNNING' | 'COMPLETED' | 'FAILED'; issues: AssessmentIssue[]; createdAt: string; completedAt?: string
}

export type KeyValueMapping = { source: string; target: string }
export type NFSMapping = { sourceServer: string; sourceExport: string; targetServer: string; targetExport: string; targetStorageClass: string }
export type NodeLabelMapping = { source: string; target?: string; action: 'MAP' | 'DROP' }
export type MappingProfileInput = {
  name: string; targetEnvironmentId: string
  storageMappings: KeyValueMapping[]; namespaceMappings: KeyValueMapping[]; ingressMappings: KeyValueMapping[]
  registryMappings: KeyValueMapping[]; nfsMappings: NFSMapping[]; nodeLabelMappings: NodeLabelMapping[]
}
export type MappingProfile = MappingProfileInput & { id: string; createdAt: string; updatedAt: string }
export type TransformDocument = {
  apiVersion: string; kind: string; namespace?: string; name: string
  sourceYaml: string; targetYaml: string; unifiedDiff: string; changed: boolean
}
export type TransformResult = { documents: TransformDocument[]; changed: number }
export type MigrationStrategy = {
  resourceMode: 'TRANSFORM'; volumeMode: 'NONE' | 'FS_BACKUP' | 'CSI_DATA_MOVER' | 'COMPOSE_KOPIA'
  preSyncEnabled: boolean; overwriteExistingResources: boolean; preserveNodePort: boolean
  preQuiesceHook?: string; postRollbackHook?: string
}
export type ValidationPolicy = {
  requireWorkloadsReady: boolean; requirePVCsBound: boolean; httpChecks?: string[]; tcpChecks?: string[]; timeoutSeconds: number
}
export type MigrationPlanInput = {
  name: string; sourceEnvironmentId: string; targetEnvironmentId: string; sourceApplicationId: string
  assessmentId: string; mappingProfileId: string; strategy: MigrationStrategy; validationPolicy: ValidationPolicy
}
export type MigrationPlan = MigrationPlanInput & { id: string; status: 'DRAFT' | 'READY' | 'BLOCKED' | 'RUNNING' | 'COMPLETED' | 'FAILED'; createdAt: string; updatedAt: string }
export type PreflightCheck = { id: string; category: string; status: 'PASSED' | 'WARNING' | 'BLOCKER'; title: string; message: string; remediation?: string }
export type PreflightResult = { migrationPlanId: string; ready: boolean; blockerCount: number; warningCount: number; checks: PreflightCheck[] }
export type MigrationRunStatus = 'PENDING' | 'PREFLIGHT' | 'PRESYNC' | 'QUIESCE' | 'FINAL_BACKUP' | 'TRANSFER' | 'TRANSFORM' | 'RESTORE' | 'VALIDATION' | 'AWAITING_CUTOVER' | 'ROLLING_BACK' | 'COMPLETED' | 'FAILED' | 'CANCELLED'
export type MigrationStep = {
  id: string; migrationRunId: string; type: 'PREFLIGHT' | 'PRESYNC' | 'QUIESCE' | 'FINAL_BACKUP' | 'TRANSFER' | 'TRANSFORM' | 'RESTORE' | 'VALIDATION' | 'AWAIT_CUTOVER' | 'ROLLBACK'
  attempt: number; status: 'PENDING' | 'RUNNING' | 'SUCCEEDED' | 'FAILED' | 'SKIPPED'; progress: number
  startedAt?: string; completedAt?: string; summary?: string; idempotencyKey: string; createdAt: string; updatedAt: string
}
export type MigrationRun = {
  id: string; migrationPlanId: string; runNumber: number; status: MigrationRunStatus; progress: number
  bytesTotal?: number; bytesTransferred?: number; startedAt?: string; completedAt?: string; errorCode?: string; errorMessage?: string
  createdAt: string; updatedAt: string
}
export type MigrationRunSnapshot = { run: MigrationRun; steps: MigrationStep[] }
export type MigrationRunSummary = MigrationRun & {
  planName: string; sourceType: 'KUBERNETES' | 'COMPOSE'; sourceEnvironmentName: string; targetEnvironmentName: string
  applicationName: string; applicationNamespace?: string
}
export type VolumeTransfer = {
  id: string; migrationRunId: string; engine: 'VELERO_FSB' | 'CSI_DATA_MOVER' | 'COMPOSE_KOPIA'
  namespace: string; sourceVolume: string; targetVolume: string; totalBytes: number; transferredBytes: number
  throughputBytesPerSecond: number; retryCount: number; checksumStatus: string
  status: 'PENDING' | 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED'; errorMessage?: string
  createdAt: string; updatedAt: string
}
export type MigrationEvent = {
  id: number; migrationRunId: string; type: string; severity: 'INFO' | 'WARNING' | 'ERROR'; message: string
  detail?: Record<string, unknown>; createdAt: string
}
export type ResourceMigrationStatus = 'DISCOVERED' | 'PLANNED' | 'CREATED' | 'SUCCEEDED' | 'WARNING' | 'FAILED' | 'MISSING' | 'SKIPPED' | 'UNKNOWN'
export type MappingChange = { type: string; path?: string; sourceValue: string; targetValue: string; changed: boolean; applied?: boolean }
export type TopologyNode = {
  id: string; side: 'SOURCE' | 'TARGET'; apiVersion?: string; kind: string; namespace?: string; name: string
  required: boolean; status: ResourceMigrationStatus; health?: string; message?: string
  attributes?: Record<string, unknown>; mappingChanges?: MappingChange[]
}
export type TopologyEdge = { id: string; from: string; to: string; relation: string; required: boolean; status?: ResourceMigrationStatus; mapping?: boolean }
export type TopologyGraph = { name: string; type: 'KUBERNETES' | 'COMPOSE'; namespace?: string; nodes: TopologyNode[]; edges: TopologyEdge[] }
export type ResourceMapping = { id: string; sourceNodeId: string; targetNodeId?: string; relation: string; changes?: MappingChange[] }
export type CurrentTopologyObservation = { graph: TopologyGraph; checkedAt: string; error?: string; drifted: number }
export type TopologyEvidence = {
  migrationRunId: string; source: TopologyGraph; target: TopologyGraph; mappings: ResourceMapping[]; snapshotOrigin: string
  sourceCapturedAt: string; targetCapturedAt?: string; currentObservation?: CurrentTopologyObservation; evidenceLimitations?: string[]
}
export type StepAttempt = {
  stepId: string; attempt: number; status: 'RUNNING' | 'SUCCEEDED' | 'RETRY_SCHEDULED' | 'FAILED'; startedAt: string
  completedAt?: string; heartbeatAt?: string; leaseExpiresAt?: string; nextAttemptAt?: string; errorCode?: string; errorMessage?: string; diagnostic?: string
}
export type TimelineStep = { step: MigrationStep; attempts: StepAttempt[]; events: MigrationEvent[] }
export type StepTimeline = {
  migrationRunId: string; generatedAt: string; steps: TimelineStep[]; events: MigrationEvent[]
  diagnosis: { state: string; title: string; reason?: string; remediation?: string; failedStepId?: string; lastSuccessfulStep?: string; lastEventAt?: string }
}

export function registerCompose(environmentId: string, composeFile: File, environmentFile?: File, projectName?: string) {
  const body = new FormData()
  body.append('compose', composeFile)
  if (environmentFile) body.append('environment', environmentFile)
  if (projectName) body.append('projectName', projectName)
  body.append('environmentId', environmentId)
  return request<SourceApplication>('/compose/analyze', { method: 'POST', body })
}

export function listEnvironmentNamespaces(id: string) {
  return request<string[]>(`/environments/${id}/namespaces`)
}

export function discoverKubernetesApplication(environmentId: string, namespace: string) {
  return request<SourceApplication>('/applications/discover', { method: 'POST', body: JSON.stringify({ environmentId, namespace }) })
}

export function previewKubernetesApplication(environmentId: string, namespace: string) {
  return request<KubernetesInventory>('/applications/preview', { method: 'POST', body: JSON.stringify({ environmentId, namespace }) })
}

export function discoverSelectedKubernetesApplication(environmentId: string, namespace: string, name: string, resources: ResourceReference[]) {
  const references = resources.map(({ apiVersion, kind, namespace, name }) => ({ apiVersion, kind, namespace, name }))
  return request<SourceApplication>('/applications/discover', { method: 'POST', body: JSON.stringify({ environmentId, namespace, name, resources: references }) })
}

export function discoverAllKubernetesApplications(environmentId: string) {
  return request<SourceApplication[]>('/applications/discover-all', { method: 'POST', body: JSON.stringify({ environmentId }) })
}

export function discoverComposeApplications(environmentId: string) {
  return request<SourceApplication[]>('/applications/discover-compose', { method: 'POST', body: JSON.stringify({ environmentId }) })
}

export function listApplications(environmentId: string) {
  return request<SourceApplication[]>(`/applications?environmentId=${encodeURIComponent(environmentId)}`)
}

export type CredentialType = 'KUBECONFIG' | 'SSH' | 'REGISTRY' | 'S3'
export type CredentialMetadata = { id: string; name: string; type: CredentialType; createdAt: string; updatedAt: string }

export function listCredentials() {
  return request<CredentialMetadata[]>('/credentials')
}

export function createCredential(name: string, type: CredentialType, payload: string) {
  return request<CredentialMetadata>('/credentials', { method: 'POST', body: JSON.stringify({ name, type, payload }) })
}

export function deleteCredential(id: string) {
  return request<void>(`/credentials/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function createAssessment(applicationId: string, targetEnvironmentId: string) {
  return request<Assessment>('/assessments', { method: 'POST', body: JSON.stringify({ applicationId, targetEnvironmentId }) })
}

export function getAssessment(id: string) {
  return request<Assessment>(`/assessments/${id}`)
}

export function listMappingProfiles(targetEnvironmentId?: string) {
  const query = targetEnvironmentId ? `?targetEnvironmentId=${encodeURIComponent(targetEnvironmentId)}` : ''
  return request<MappingProfile[]>(`/mapping-profiles${query}`)
}

export function createMappingProfile(input: MappingProfileInput) {
  return request<MappingProfile>('/mapping-profiles', { method: 'POST', body: JSON.stringify(input) })
}

export function updateMappingProfile(id: string, input: MappingProfileInput) {
  return request<MappingProfile>(`/mapping-profiles/${id}`, { method: 'PUT', body: JSON.stringify(input) })
}

export function deleteMappingProfile(id: string) {
  return request<void>(`/mapping-profiles/${id}`, { method: 'DELETE' })
}

export function previewManifestTransform(profileId: string, manifests: File) {
  const body = new FormData()
  body.append('profileId', profileId)
  body.append('manifests', manifests)
  return request<TransformResult>('/transforms/preview', { method: 'POST', body })
}

export function createMigrationPlan(input: MigrationPlanInput) {
  return request<MigrationPlan>('/migration-plans', { method: 'POST', body: JSON.stringify(input) })
}

export function getMigrationPlan(id: string) { return request<MigrationPlan>(`/migration-plans/${id}`) }
export function deleteMigrationRun(id: string) { return request<void>(`/migration-runs/${id}`, { method: 'DELETE' }) }

export function preflightMigrationPlan(id: string) {
  return request<PreflightResult>(`/migration-plans/${id}/preflight`, { method: 'POST' })
}

export function startMigrationRun(planId: string) {
  return request<MigrationRunSnapshot>(`/migration-plans/${planId}/runs`, { method: 'POST' })
}

export function getMigrationRun(runId: string) {
  return request<MigrationRunSnapshot>(`/migration-runs/${runId}`)
}

export function listMigrationRuns() {
  return request<MigrationRunSummary[]>('/migration-runs')
}

export function listMigrationVolumeTransfers(runId: string) {
  return request<VolumeTransfer[]>(`/migration-runs/${runId}/volume-transfers`)
}

export function getMigrationTopology(runId: string) {
  return request<TopologyEvidence>(`/migration-runs/${runId}/topology`)
}

export function refreshMigrationTopology(runId: string) {
  return request<TopologyEvidence>(`/migration-runs/${runId}/topology/refresh`, { method: 'POST' })
}

export function getMigrationTimeline(runId: string) {
  return request<StepTimeline>(`/migration-runs/${runId}/timeline`)
}

export function cancelMigrationRun(runId: string) {
  return request<MigrationRunSnapshot>(`/migration-runs/${runId}/cancel`, { method: 'POST' })
}

export function retryMigrationRun(runId: string) {
  return request<MigrationRunSnapshot>(`/migration-runs/${runId}/retry`, { method: 'POST' })
}

export function confirmMigrationCutover(runId: string) {
  return request<void>(`/migration-runs/${runId}/cutover`, { method: 'POST' })
}

export function rollbackMigrationRun(runId: string) {
  return request<MigrationRunSnapshot>(`/migration-runs/${runId}/rollback`, { method: 'POST' })
}

export function restoreMigrationSource(runId: string) {
  return request<MigrationRunSnapshot>(`/migration-runs/${runId}/restore-source`, { method: 'POST' })
}

export function migrationReportURL(runId: string) {
  return `/api/v1/migration-runs/${encodeURIComponent(runId)}/report`
}

export function cleanupMigrationArtifacts(runId: string) {
  return request<{ migrationRunId: string; clusters: Array<{ environmentId: string; role: 'SOURCE' | 'TARGET'; backupsDeleted: number; restoresDeleted: number }>; retained?: string[]; cleanedAt: string }>(`/migration-runs/${runId}/cleanup`, { method: 'POST' })
}

export function migrationEventSource(runId: string) {
  return new EventSource(`/api/v1/migration-runs/${encodeURIComponent(runId)}/events`, { withCredentials: true })
}

export function testEnvironmentConnection(id: string) {
  return request<ConnectionTest>(`/environments/${id}/test`, { method: 'POST' })
}

export function deleteEnvironment(id: string) {
  return request<void>(`/environments/${id}`, { method: 'DELETE' })
}

export function refreshEnvironmentCapabilities(id: string) {
  return request<ClusterCapabilities>(`/environments/${id}/capabilities`, { method: 'POST' })
}

function readCookie(name: string): string {
  const prefix = `${encodeURIComponent(name)}=`
  const item = document.cookie.split('; ').find((value) => value.startsWith(prefix))
  return item ? decodeURIComponent(item.slice(prefix.length)) : ''
}
