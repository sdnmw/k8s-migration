import { lazy, Suspense, type ReactNode } from 'react'
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { App as AntApplication, Spin } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { getCurrentAdministrator } from './api/client'
import AppShell from './layout/AppShell'
import LoginPage from './pages/LoginPage'

const OverviewPage = lazy(() => import('./pages/OverviewPage'))
const MigrationsPage = lazy(() => import('./pages/MigrationsPage'))
const NewMigrationPage = lazy(() => import('./pages/NewMigrationPage'))
const EnvironmentPage = lazy(() => import('./pages/EnvironmentPage'))
const MappingProfilesPage = lazy(() => import('./pages/MappingProfilesPage'))
const TransformPreviewPage = lazy(() => import('./pages/TransformPreviewPage'))
const MigrationRunDetailPage = lazy(() => import('./pages/MigrationRunDetailPage'))
const StorageProfilesPage = lazy(() => import('./pages/StorageProfilesPage'))
const ObjectStoragePage = lazy(() => import('./pages/ObjectStoragePage'))
const CredentialsPage = lazy(() => import('./pages/CredentialsPage'))

function ProtectedApplication() {
  const location = useLocation()
  const currentAdministrator = useQuery({
    queryKey: ['current-administrator'],
    queryFn: getCurrentAdministrator,
    retry: false,
    staleTime: 60_000,
  })

  if (currentAdministrator.isPending) {
    return (
      <div className="full-page-state" aria-label="正在加载">
        <Spin size="large" />
      </div>
    )
  }
  if (currentAdministrator.isError) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return (
    <AppShell administrator={currentAdministrator.data}>
      <Suspense fallback={<div className="content-loading"><Spin /></div>}>
        <Outlet />
      </Suspense>
    </AppShell>
  )
}

export default function App() {
  return (
    <AntApplication><Routes>
      <Route path="/login" element={<LoginPage />} />
      {import.meta.env.DEV && (
        <Route path="/__preview">
          <Route index element={<DevelopmentPreview><OverviewPage preview /></DevelopmentPreview>} />
          <Route path="new" element={<DevelopmentPreview><NewMigrationPage preview /></DevelopmentPreview>} />
          <Route path="detail" element={<DevelopmentPreview><MigrationRunDetailPage preview /></DevelopmentPreview>} />
          <Route path="run" element={<DevelopmentPreview><MigrationRunDetailPage preview /></DevelopmentPreview>} />
          <Route path="environments/sources" element={<DevelopmentPreview><EnvironmentPage role="SOURCE" preview /></DevelopmentPreview>} />
          <Route path="environments/sks" element={<DevelopmentPreview><EnvironmentPage role="TARGET" preview /></DevelopmentPreview>} />
          <Route path="mappings" element={<DevelopmentPreview><MappingProfilesPage preview /></DevelopmentPreview>} />
          <Route path="transforms" element={<DevelopmentPreview><TransformPreviewPage preview /></DevelopmentPreview>} />
          <Route path="storage-profiles" element={<DevelopmentPreview><StorageProfilesPage preview /></DevelopmentPreview>} />
          <Route path="object-storage" element={<DevelopmentPreview><ObjectStoragePage preview /></DevelopmentPreview>} />
        </Route>
      )}
      <Route element={<ProtectedApplication />}>
        <Route index element={<OverviewPage />} />
        <Route path="migrations" element={<MigrationsPage />} />
        <Route path="migrations/new" element={<NewMigrationPage />} />
        <Route path="migrations/:id" element={<MigrationRunDetailPage />} />
        <Route path="environments/sources" element={<EnvironmentPage role="SOURCE" />} />
        <Route path="environments/sks" element={<EnvironmentPage role="TARGET" />} />
        <Route path="strategies/storage" element={<MappingProfilesPage key="storage-mappings" initialTab="storage" />} />
        <Route path="strategies/registry" element={<MappingProfilesPage key="registry-mappings" initialTab="registry" />} />
        <Route path="strategies/transforms" element={<TransformPreviewPage />} />
        <Route path="settings/object-storage" element={<ObjectStoragePage />} />
        <Route path="settings/storage-profiles" element={<StorageProfilesPage />} />
        <Route path="settings/credentials" element={<CredentialsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes></AntApplication>
  )
}

function DevelopmentPreview({ children }: { children: ReactNode }) {
  return (
    <AppShell administrator={{ id: 'development-preview', username: 'admin' }}>
      <Suspense fallback={<div className="content-loading"><Spin /></div>}>{children}</Suspense>
    </AppShell>
  )
}
