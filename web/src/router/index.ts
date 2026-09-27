import { createRouter, createWebHistory, type Router, type RouteRecordRaw, type RouterHistory } from 'vue-router'

import type { Role } from '@/stores/session'

import { resolveNavigation } from './guards'

declare module 'vue-router' {
  interface RouteMeta {
    titleKey?: string
    /** Reachable without signing in. */
    public?: boolean
    /** "bare" pages render without the app shell (setup, login, forced account steps). */
    layout?: 'bare' | 'app'
    /** Minimum role; lower roles are sent home. */
    role?: Role
    /** Editors that need the width: the sidebar starts collapsed there (see useSidebarPreference). */
    wide?: boolean
  }
}


export const routes: RouteRecordRaw[] = [
  { path: '/setup', name: 'setup', component: () => import('@/views/SetupView.vue'), meta: { public: true, layout: 'bare', titleKey: 'setup.title' } },
  { path: '/forgot-password', name: 'forgot-password', component: () => import('@/views/ForgotPasswordView.vue'), meta: { public: true, layout: 'bare', titleKey: 'forgotPassword.title' } },
  { path: '/reset-password', name: 'reset-password', component: () => import('@/views/ResetPasswordView.vue'), meta: { public: true, layout: 'bare', titleKey: 'resetPassword.title' } },
  { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { public: true, layout: 'bare', titleKey: 'login.title' } },
  { path: '/account/password', name: 'change-password', component: () => import('@/views/ChangePasswordView.vue'), meta: { layout: 'bare', titleKey: 'changePassword.title' } },
  { path: '/account/two-factor', name: 'two-factor-setup', component: () => import('@/views/TwoFactorSetupView.vue'), meta: { layout: 'bare', titleKey: 'twoFactorSetup.title' } },
  { path: '/', name: 'home', component: () => import('@/views/HomeView.vue'), meta: { titleKey: 'nav.home' } },
  { path: '/reports', name: 'reports', component: () => import('@/views/reports/ReportsView.vue'), meta: { titleKey: 'nav.reports' } },
  { path: '/reports/new', name: 'report-new', component: () => import('@/views/reports/ReportEditorView.vue'), meta: { titleKey: 'reports.add', role: 'editor', wide: true } },
  { path: '/reports/:id', name: 'report', component: () => import('@/views/reports/ReportDetailView.vue'), meta: { titleKey: 'nav.reports' } },
  { path: '/reports/:id/edit', name: 'report-edit', component: () => import('@/views/reports/ReportEditorView.vue'), meta: { titleKey: 'reports.editor.editTitle', role: 'editor', wide: true } },
  { path: '/runs', name: 'runs', component: () => import('@/views/runs/RunsView.vue'), meta: { titleKey: 'nav.runs' } },
  { path: '/runs/:id', name: 'run', component: () => import('@/views/runs/RunDetailView.vue'), meta: { titleKey: 'nav.runs' } },
  { path: '/queries', name: 'queries', component: () => import('@/views/queries/QueriesView.vue'), meta: { titleKey: 'nav.queries' } },
  { path: '/queries/new', name: 'query-new', component: () => import('@/views/queries/QueryEditorView.vue'), meta: { titleKey: 'queries.add', role: 'editor', wide: true } },
  { path: '/queries/:id', name: 'query', component: () => import('@/views/queries/QueryEditorView.vue'), meta: { titleKey: 'nav.queries', wide: true } },
  { path: '/connections', name: 'connections', component: () => import('@/views/connections/ConnectionsView.vue'), meta: { titleKey: 'nav.connections' } },
  { path: '/connections/new', name: 'connection-new', component: () => import('@/views/connections/ConnectionFormView.vue'), meta: { titleKey: 'connections.add', role: 'admin' } },
  { path: '/connections/:id', name: 'connection', component: () => import('@/views/connections/ConnectionDetailView.vue'), meta: { titleKey: 'nav.connections' } },
  { path: '/connections/:id/edit', name: 'connection-edit', component: () => import('@/views/connections/ConnectionFormView.vue'), meta: { titleKey: 'nav.connections', role: 'admin' } },
  { path: '/channels', name: 'channels', component: () => import('@/views/channels/ChannelsView.vue'), meta: { titleKey: 'nav.channels' } },
  { path: '/channels/new', name: 'channel-new', component: () => import('@/views/channels/ChannelFormView.vue'), meta: { titleKey: 'channels.add', role: 'admin' } },
  { path: '/channels/:id', name: 'channel', component: () => import('@/views/channels/ChannelDetailView.vue'), meta: { titleKey: 'nav.channels' } },
  { path: '/channels/:id/edit', name: 'channel-edit', component: () => import('@/views/channels/ChannelFormView.vue'), meta: { titleKey: 'nav.channels', role: 'admin' } },
  { path: '/links', name: 'links', component: () => import('@/views/links/LinksView.vue'), meta: { titleKey: 'nav.links' } },
  { path: '/profile', name: 'profile', component: () => import('@/views/ProfileView.vue'), meta: { titleKey: 'profile.title' } },
  {
    path: '/settings',
    component: () => import('@/views/settings/SettingsLayout.vue'),
    redirect: { name: 'settings-general' },
    children: [
      { path: 'general', name: 'settings-general', component: () => import('@/views/settings/GeneralSettingsView.vue'), meta: { titleKey: 'settings.general.title' } },
      { path: 'users', name: 'settings-users', component: () => import('@/views/settings/UsersView.vue'), meta: { titleKey: 'settings.users.title' } },
      { path: 'import-export', name: 'settings-import-export', component: () => import('@/views/settings/ImportExportView.vue'), meta: { titleKey: 'config.title', role: 'admin' } },
      { path: 'ai', name: 'settings-ai', component: () => import('@/views/settings/AISettingsView.vue'), meta: { titleKey: 'settings.ai.title', role: 'admin' } },
      { path: 'about', name: 'settings-about', component: () => import('@/views/settings/AboutSettingsView.vue'), meta: { titleKey: 'settings.about.title' } },
      { path: 'storage', name: 'settings-storage', component: () => import('@/views/settings/StorageSettingsView.vue'), meta: { titleKey: 'settings.storage.title', role: 'admin' } },
      { path: 'alerts', name: 'settings-alerts', component: () => import('@/views/settings/AlertsSettingsView.vue'), meta: { titleKey: 'settings.alerts.title', role: 'admin' } },
      { path: 'api-keys', name: 'settings-api-keys', component: () => import('@/views/settings/ApiKeysView.vue'), meta: { titleKey: 'settings.apiKeys.title', role: 'admin' } },
      { path: 'security', name: 'settings-security', component: () => import('@/views/settings/SecurityView.vue'), meta: { titleKey: 'settings.security.title', role: 'admin' } },
    ],
  },
  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('@/views/NotFoundView.vue'), meta: { titleKey: 'notFound.title' } },
]

export function createAppRouter(history: RouterHistory = createWebHistory()) {
  return createRouter({ history, routes })
}

interface SessionLike {
  bootstrap(): Promise<void>
  setupRequired: boolean
  me: { role: Role; restriction: string } | null
}

/** Sends every navigation through resolveNavigation after the session is known. */
export function installGuards(router: Router, session: SessionLike) {
  router.beforeEach(async (to) => {
    await session.bootstrap()
    return resolveNavigation({ setupRequired: session.setupRequired, me: session.me as never }, to) ?? true
  })
}
