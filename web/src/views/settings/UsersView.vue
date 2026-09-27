<script setup lang="ts">
import { UserPlus, Users } from '@lucide/vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import CopyField from '@/components/common/CopyField.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useApiError } from '@/composables/useApiError'
import { useFormat } from '@/composables/useFormat'
import { useSessionStore } from '@/stores/session'

type User = Schemas['User']
type Role = Schemas['Role']

const session = useSessionStore()
const { t, te } = useI18n()
const fmt = useFormat()
const isAdmin = computed(() => session.hasRole('admin'))
const users = ref<User[]>([])
const nextCursor = ref<string | null>(null)
const roles: Role[] = ['viewer', 'editor', 'admin']
const roleOptions = computed(() => roles.map((r) => ({ value: r, label: t(`roles.${r}`) })))

async function load(more = false) {
  try {
    const page = unwrap(await api.GET('/api/v1/users', { params: { query: { cursor: more ? (nextCursor.value ?? undefined) : undefined } } }))
    users.value = more ? [...users.value, ...page.items] : page.items
    nextCursor.value = page.next_cursor ?? null
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
onMounted(() => load())

// Create
const createOpen = ref(false)
const createErr = useApiError()
const newUser = reactive({ name: '', email: '', role: 'viewer' as Role })
const creating = ref(false)
const revealed = ref<{ title: string; email: string; password: string } | null>(null)

function openCreate() {
  Object.assign(newUser, { name: '', email: '', role: 'viewer' })
  createErr.clear()
  createOpen.value = true
}

async function create() {
  creating.value = true
  createErr.clear()
  try {
    const res = unwrap(await api.POST('/api/v1/users', { body: { ...newUser } }))
    createOpen.value = false
    revealed.value = { title: t('settings.users.created'), email: res.user.email, password: res.temporary_password }
    await load()
  } catch (e) {
    createErr.error.value = e
  } finally {
    creating.value = false
  }
}

// Row actions, each confirmed with its impact.
type Action = 'role' | 'disable' | 'enable' | 'reset' | 'disable2fa'
const pending = ref<{ action: Action; user: User; role?: Role } | null>(null)
const confirmOpen = computed({
  get: () => pending.value !== null,
  set: (v) => {
    if (!v) pending.value = null
  },
})

const confirmText = computed(() => {
  const p = pending.value
  if (!p) return { title: '', description: '', label: '', destructive: false }
  const who = { name: p.user.name, email: p.user.email }
  switch (p.action) {
    case 'role':
      return { title: t('settings.users.changeRole'), description: t('settings.users.changeRoleImpact', { ...who, role: t(`roles.${p.role}`) }), label: t('settings.users.changeRole'), destructive: false }
    case 'disable':
      return { title: t('settings.users.disable'), description: t('settings.users.disableImpact', who), label: t('settings.users.disable'), destructive: true }
    case 'enable':
      return { title: t('settings.users.enable'), description: t('settings.users.enableImpact', who), label: t('settings.users.enable'), destructive: false }
    case 'reset':
      return { title: t('settings.users.resetPassword'), description: t('settings.users.resetImpact', who), label: t('settings.users.resetPassword'), destructive: true }
    case 'disable2fa':
      return { title: t('settings.users.disable2fa'), description: t('settings.users.disable2faImpact', who), label: t('settings.users.disable2fa'), destructive: true }
  }
  return { title: '', description: '', label: '', destructive: false }
})

async function runAction() {
  const p = pending.value
  if (!p) return
  const path = { params: { path: { userId: p.user.id } } }
  try {
    switch (p.action) {
      case 'role':
        unwrap(await api.PATCH('/api/v1/users/{userId}', { ...path, body: { version: p.user.version, role: p.role } }))
        break
      case 'disable':
      case 'enable':
        unwrap(await api.PATCH('/api/v1/users/{userId}', { ...path, body: { version: p.user.version, disabled: p.action === 'disable' } }))
        break
      case 'reset': {
        const res = unwrap(await api.POST('/api/v1/users/{userId}/reset-password', path))
        revealed.value = { title: t('settings.users.resetDone'), email: p.user.email, password: res.temporary_password }
        break
      }
      case 'disable2fa':
        unwrap(await api.POST('/api/v1/users/{userId}/2fa/disable', path))
        break
    }
    toast.success(t('settings.users.updated'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
  await load()
}

function onRoleChange(user: User, role: string) {
  if (role !== user.role) pending.value = { action: 'role', user, role: role as Role }
}
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="text-muted-foreground text-sm">{{ t('settings.users.help') }}</p>
      <Button v-if="isAdmin" data-testid="user-create" @click="openCreate">
        <UserPlus aria-hidden="true" />
        {{ t('settings.users.add') }}
      </Button>
    </div>

    <EmptyState v-if="users.length === 0" :icon="Users" :title="t('settings.users.emptyTitle')" :description="t('settings.users.emptyBody')" />
    <div v-else class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('fields.name') }}</TableHead>
            <TableHead>{{ t('settings.users.role') }}</TableHead>
            <TableHead>{{ t('settings.users.status') }}</TableHead>
            <TableHead>{{ t('settings.users.lastLogin') }}</TableHead>
            <TableHead v-if="isAdmin"><span class="sr-only">{{ t('common.actions') }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="u in users" :key="u.id" :data-testid="`user-row-${u.email}`">
            <TableCell>
              <div class="font-medium">{{ u.name }}</div>
              <div class="text-muted-foreground text-xs">{{ u.email }}</div>
            </TableCell>
            <TableCell class="min-w-32">
              <NativeSelect
                v-if="isAdmin && u.id !== session.me?.id"
                :id="`role-${u.id}`"
                :model-value="u.role"
                :options="roleOptions"
                :aria-label="t('settings.users.role')"
                @update:model-value="(r: string) => onRoleChange(u, r)"
              />
              <span v-else>{{ t(`roles.${u.role}`) }}</span>
            </TableCell>
            <TableCell class="flex flex-wrap gap-1">
              <Badge v-if="u.disabled" variant="destructive">{{ t('settings.users.disabled') }}</Badge>
              <Badge v-else-if="u.locked" variant="destructive">{{ t('settings.users.locked') }}</Badge>
              <Badge v-else variant="secondary">{{ t('settings.users.active') }}</Badge>
              <Badge v-if="u.totp_enabled" variant="outline">{{ t('twoFactor.short') }}</Badge>
              <Badge v-if="u.must_change_password" variant="outline">{{ t('settings.users.temporaryPassword') }}</Badge>
            </TableCell>
            <TableCell class="text-muted-foreground text-sm">{{ u.last_login_at ? fmt.dateTime(u.last_login_at) : t('settings.users.never') }}</TableCell>
            <TableCell v-if="isAdmin" class="text-right">
              <DropdownMenu v-if="u.id !== session.me?.id">
                <DropdownMenuTrigger as-child>
                  <Button variant="ghost" size="sm" :data-testid="`user-actions-${u.email}`">{{ t('common.actions') }}</Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem @select="pending = { action: 'reset', user: u }">{{ t('settings.users.resetPassword') }}</DropdownMenuItem>
                  <DropdownMenuItem v-if="u.totp_enabled" @select="pending = { action: 'disable2fa', user: u }">{{ t('settings.users.disable2fa') }}</DropdownMenuItem>
                  <DropdownMenuItem v-if="u.disabled" @select="pending = { action: 'enable', user: u }">{{ t('settings.users.enable') }}</DropdownMenuItem>
                  <DropdownMenuItem v-else class="text-destructive" @select="pending = { action: 'disable', user: u }">{{ t('settings.users.disable') }}</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
    <div v-if="nextCursor"><Button variant="outline" @click="load(true)">{{ t('common.loadMore') }}</Button></div>

    <Dialog v-model:open="createOpen">
      <DialogContent>
        <form class="grid gap-4" @submit.prevent="create">
          <DialogHeader>
            <DialogTitle>{{ t('settings.users.add') }}</DialogTitle>
            <DialogDescription>{{ t('settings.users.addHelp') }}</DialogDescription>
          </DialogHeader>
          <FormField id="new-user-name" :label="t('fields.name')" :error="createErr.field('name')">
            <Input id="new-user-name" v-model="newUser.name" required />
          </FormField>
          <FormField id="new-user-email" :label="t('fields.email')" :error="createErr.field('email')">
            <Input id="new-user-email" v-model="newUser.email" type="email" required />
          </FormField>
          <FormField id="new-user-role" :label="t('settings.users.role')" :hint="t(`roles.${newUser.role}Help`)" :error="createErr.field('role')">
            <NativeSelect id="new-user-role" v-model="newUser.role" :options="roleOptions" />
          </FormField>
          <p v-if="createErr.message.value && !createErr.hasFieldErrors.value" class="text-destructive text-sm">{{ createErr.message.value }}</p>
          <DialogFooter>
            <Button type="submit" :disabled="creating" data-testid="user-create-submit">{{ t('settings.users.add') }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="revealed !== null" @update:open="(v) => { if (!v) revealed = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ revealed?.title }}</DialogTitle>
          <DialogDescription>{{ t('settings.users.temporaryPasswordHelp', { email: revealed?.email }) }}</DialogDescription>
        </DialogHeader>
        <Alert>
          <AlertDescription>{{ t('common.shownOnce') }}</AlertDescription>
        </Alert>
        <CopyField v-if="revealed" :value="revealed.password" :label="t('settings.users.temporaryPassword')" />
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="confirmText.title"
      :description="confirmText.description"
      :confirm-label="confirmText.label"
      :destructive="confirmText.destructive"
      :on-confirm="runAction"
    />
  </div>
</template>
