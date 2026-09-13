import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import type { StepTimeline } from './api/client'
import MigrationExecutionTimeline from './components/MigrationExecutionTimeline'

describe('App', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('renders the authenticated infrastructure shell and overview', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => Promise.resolve(new Response(JSON.stringify(String(input).endsWith('/auth/me') ? { id: 'admin-id', username: 'admin' } : []), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))))
    renderApp('/')
    expect(await screen.findByRole('heading', { name: '概览' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'SKS Migration Center 首页' })).toBeInTheDocument()
    expect(screen.getByText('最近迁移任务')).toBeInTheDocument()
    expect(screen.getByText('SmartX SKS')).toBeInTheDocument()
    expect(await screen.findByText('暂无迁移任务')).toBeInTheDocument()
    expect(screen.queryByText('迁移服务正常')).not.toBeInTheDocument()
  })

  it('opens the administrator password change dialog from the account menu', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => Promise.resolve(new Response(JSON.stringify(String(input).endsWith('/auth/me') ? { id: 'admin-id', username: 'admin' } : []), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))))
    renderApp('/')
    fireEvent.click(await screen.findByRole('button', { name: /admin/ }))
    fireEvent.click(await screen.findByText('修改密码'))
    expect(await screen.findByRole('dialog', { name: '修改管理员密码' })).toBeInTheDocument()
    expect(screen.getByLabelText('当前密码')).toBeInTheDocument()
    expect(screen.getByLabelText('新密码')).toBeInTheDocument()
    expect(screen.getByLabelText('确认新密码')).toBeInTheDocument()
    expect(screen.getByText(/当前及其他已登录会话都会退出/)).toBeInTheDocument()
  })

  it('renders migration tasks from the API instead of fixture data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const path = String(input)
      const body = path.endsWith('/auth/me') ? { id: 'admin-id', username: 'admin' } : path.endsWith('/migration-runs') ? [{
        id: '10000000-0000-4000-8000-000000000001', migrationPlanId: '20000000-0000-4000-8000-000000000001', runNumber: 1,
        planName: 'API 返回的迁移', sourceType: 'KUBERNETES', sourceEnvironmentName: 'sida', targetEnvironmentName: 'mw',
        applicationName: 'orders', applicationNamespace: 'orders', status: 'AWAITING_CUTOVER', progress: 96,
        bytesTotal: 1024, bytesTransferred: 1024, startedAt: '2026-09-05T00:00:00Z', createdAt: '2026-09-05T00:00:00Z', updatedAt: '2026-09-05T00:01:00Z',
      }] : []
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    }))
    renderApp('/migrations')
    expect(await screen.findByText('API 返回的迁移')).toBeInTheDocument()
    expect(screen.getByText('等待切流')).toBeInTheDocument()
    expect(screen.getByText('sida')).toBeInTheDocument()
    expect(screen.queryByText('生产订单服务迁移')).not.toBeInTheDocument()
  })

  it('groups terminal migration actions in the overflow menu', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const path = String(input)
      const body = path.endsWith('/auth/me') ? { id: 'admin-id', username: 'admin' } : path.endsWith('/migration-runs') ? [{
        id: '10000000-0000-4000-8000-000000000002', migrationPlanId: '20000000-0000-4000-8000-000000000002', runNumber: 6,
        planName: 'harbor', sourceType: 'COMPOSE', sourceEnvironmentName: 'harbor', targetEnvironmentName: 'mw',
        applicationName: 'harbor', status: 'COMPLETED', progress: 100, bytesTransferred: 1024,
        startedAt: '2026-09-10T00:00:00Z', completedAt: '2026-09-10T01:00:00Z', createdAt: '2026-09-10T00:00:00Z', updatedAt: '2026-09-10T01:00:00Z',
      }] : []
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    }))
    renderApp('/migrations')
    fireEvent.click(await screen.findByRole('button', { name: 'harbor #6 更多操作' }))
    expect(await screen.findByText('查看详情')).toBeInTheDocument()
    expect(screen.getByText('重新执行')).toBeInTheDocument()
    expect(screen.getByText('编辑任务')).toBeInTheDocument()
    expect(screen.getByText('删除任务')).toBeInTheDocument()
  })

  it('renders the local administrator login page', () => {
    renderApp('/login')
    expect(screen.getByRole('heading', { name: '管理员登录' })).toBeInTheDocument()
    expect(screen.getByLabelText('用户名')).toHaveValue('admin')
    expect(screen.getByRole('button', { name: /登\s*录/ })).toBeInTheDocument()
  })

  it('keeps the migration wizard gated until a source type is selected', async () => {
    renderApp('/__preview/new')
    const next = await screen.findByRole('button', { name: '下一步' })
    expect(next).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /Kubernetes → SKS/ }))
    expect(next).toBeEnabled()
    expect(screen.getAllByText(/源环境|选择应用|迁移评估|目标映射|迁移检查|确认/).length).toBeGreaterThanOrEqual(6)
  })

  it('completes the six-step migration plan wizard through preflight', async () => {
    renderApp('/__preview/new')
    fireEvent.click(await screen.findByRole('button', { name: /Kubernetes → SKS/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByText('business · business'))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByText('SKS-Production · v1.32.6'))
    fireEvent.click(screen.getByRole('button', { name: '执行评估' }))
    expect(await screen.findByText('评估门禁通过，可以继续配置目标映射。')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))

    fireEvent.change(screen.getByPlaceholderText('例如：生产订单服务迁移'), { target: { value: '生产订单服务迁移' } })
    fireEvent.mouseDown(screen.getAllByRole('combobox')[0])
    fireEvent.click(await screen.findByText('生产默认映射'))
    fireEvent.click(screen.getByRole('button', { name: '保存并执行检查' }))
    expect(await screen.findByText('所有阻塞门禁已通过')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    expect(await screen.findByText('计划已保存并通过 Preflight')).toBeInTheDocument()
    const confirm = screen.getByRole('button', { name: '确认并启动' })
    expect(confirm).toBeDisabled()
    fireEvent.click(screen.getByRole('checkbox'))
    expect(confirm).toBeEnabled()
		fireEvent.click(confirm)
		expect(await screen.findByRole('heading', { name: '迁移任务 #3' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '资源拓扑' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '执行时序' })).toBeInTheDocument()
  })

  it('lists imported Kubernetes environments without exposing credentials', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const path = String(input)
      const body = path.endsWith('/auth/me')
        ? { id: 'admin-id', username: 'admin' }
        : [{
            id: 'source-id', name: '生产 Kubernetes', role: 'SOURCE', kind: 'KUBERNETES',
            endpoint: 'https://k8s.example.test:6443', status: 'CONNECTED', statusMessage: '连接正常',
            capabilities: { kubernetesVersion: 'v1.36.2', nodeCount: 3, namespaceCount: 8 },
            createdAt: '2026-09-02T00:00:00Z', updatedAt: '2026-09-02T00:00:00Z',
          }]
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    }))
    renderApp('/environments/sources')
    expect(await screen.findByText('生产 Kubernetes')).toBeInTheDocument()
    expect(screen.getByText('v1.36.2')).toBeInTheDocument()
    expect(screen.getByText('3 节点 · 8 命名空间')).toBeInTheDocument()
    expect(screen.getByText('发现应用')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /添加源环境/ }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText(/主密钥加密/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Docker Compose'))
    expect(screen.getByLabelText('SSH 地址')).toBeInTheDocument()
    expect(screen.getByLabelText('主机指纹（可选）')).toBeInTheDocument()
    expect(screen.getByLabelText('SSH 密码')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: 'SSH 私钥' }))
    expect(await screen.findByText('选择私钥文件')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/粘贴 kubeconfig/)).not.toBeInTheDocument()
  })

  it('opens the write-only credential dialog from the credentials page', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const body = String(input).endsWith('/auth/me') ? { id: 'admin-id', username: 'admin' } : []
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    }))
    renderApp('/settings/credentials')
    fireEvent.click(await screen.findByRole('button', { name: /添加凭证/ }))
    expect(await screen.findByRole('dialog', { name: '添加凭证' })).toBeInTheDocument()
    expect(screen.getByLabelText('凭证名称')).toBeInTheDocument()
    expect(screen.getByLabelText('凭证类型')).toBeInTheDocument()
    expect(screen.getByLabelText('凭证内容')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '加密保存' })).toBeInTheDocument()
  })

  it('offers automatic and exact resource selection for Kubernetes applications', async () => {
    renderApp('/__preview/new')
    fireEvent.click(await screen.findByRole('button', { name: /Kubernetes → SKS/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    expect(screen.getByText('自动发现应用')).toBeInTheDocument()
    fireEvent.click(screen.getByText('按 Namespace 手选资源'))
    expect(await screen.findByRole('button', { name: '读取资源' })).toBeDisabled()
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click((await screen.findAllByText('business')).at(-1)!)
    const readResources = await screen.findByRole('button', { name: '读取资源' })
    expect(readResources).toBeEnabled()
    fireEvent.click(readResources)
    expect(await screen.findByText('选择迁移资源')).toBeInTheDocument()
    expect(screen.getByText('Deployment')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存为迁移应用' })).toBeDisabled()
  })

  it('opens compatibility rule details from the assessment score', async () => {
    renderApp('/__preview/new')
    fireEvent.click(await screen.findByRole('button', { name: /Kubernetes → SKS/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByText('business · business'))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByText('SKS-Production · v1.32.6'))
    fireEvent.click(screen.getByRole('button', { name: '执行评估' }))
    fireEvent.click(await screen.findByText('兼容性得分 · 查看细则'))
    expect(await screen.findByText('兼容性评分细则')).toBeInTheDocument()
  })

  it('renders the three-lane migration topology and focuses related resources', async () => {
    renderApp('/__preview/detail')
    fireEvent.click(await screen.findByRole('tab', { name: '资源拓扑' }))
    expect(await screen.findByText('迁移转换')).toBeInTheDocument()
    expect(screen.getByText('源端 · sida')).toBeInTheDocument()
    expect(screen.getByText('目标端 · mw')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '应用视图' })).toBeChecked()

    fireEvent.click(await screen.findByTestId('rf__node-src-svc'))
    expect(await screen.findByText('资源详情')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(await screen.findByText('Service · 源端资源')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: /转换差异/ }))
    expect(screen.getByRole('tab', { name: /转换差异/ })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByTestId('rf__node-src-pvc')).toHaveStyle({ opacity: '0.14' })
  })

  it('renders distinct pages when switching the three migration strategy menus', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((input: RequestInfo | URL) => Promise.resolve(new Response(JSON.stringify(String(input).endsWith('/auth/me') ? { id: 'admin-id', username: 'admin' } : []), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))))
    renderApp('/strategies/storage')
    expect(await screen.findByRole('heading', { name: '资源映射' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '镜像仓库映射' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('镜像仓库'))
    expect(await screen.findByRole('heading', { name: '镜像仓库映射' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '资源映射' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('资源转换规则'))
    expect(await screen.findByRole('heading', { name: '资源转换规则' })).toBeInTheDocument()
    expect(screen.getByText(/上传 YAML 后生成预览/)).toBeInTheDocument()
  })

  it('shows a redacted deterministic manifest transform preview', async () => {
    renderApp('/__preview/transforms')
    expect(await screen.findByRole('heading', { name: '资源转换规则' })).toBeInTheDocument()
    expect(screen.getByText('3 个对象')).toBeInTheDocument()
    expect(screen.getAllByText('Deployment / api').length).toBeGreaterThan(0)
    expect(screen.getByText(/harbor\.example\.local\/migrated/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Secret / database'))
    expect(screen.getAllByText(/<redacted>/).length).toBeGreaterThan(0)
    expect(screen.queryByText('c2VjcmV0')).not.toBeInTheDocument()
  })

  it('renders an active timeline when attempt and event arrays are null', () => {
    const value = {
      migrationRunId: 'run-1', generatedAt: '2026-09-08T00:00:00Z', events: null,
      diagnosis: { state: 'WAITING', title: '等待人工切流', reason: '目标验证已通过' },
      steps: [{
        step: {
          id: 'step-1', migrationRunId: 'run-1', type: 'AWAIT_CUTOVER', attempt: 1, status: 'RUNNING', progress: 0,
          idempotencyKey: 'AWAIT_CUTOVER:v1', createdAt: '2026-09-08T00:00:00Z', updatedAt: '2026-09-08T00:00:00Z',
        },
        attempts: null, events: null,
      }],
    } as unknown as StepTimeline
    render(<MigrationExecutionTimeline value={value} />)
    expect(screen.getAllByText('等待人工切流')).toHaveLength(2)
    expect(screen.getByText('目标验证已通过')).toBeInTheDocument()
  })
})

function renderApp(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}
