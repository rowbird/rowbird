<script setup lang="ts">
import { KeyRound } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import AddPasskeyDialog from '@/components/account/AddPasskeyDialog.vue'
import PasswordConfirmDialog from '@/components/account/PasswordConfirmDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import FormField from '@/components/common/FormField.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useFormat } from '@/composables/useFormat'
import { useSessionStore } from '@/stores/session'

/** The caller's passkeys (docs/spec/06-ui.md, "Profile"): list, add, rename and remove. */
const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const keys = ref<Schemas['Passkey'][]>([])
const adding = ref(false)
const renaming = ref<Schemas['Passkey'] | null>(null)
const newName = ref('')
const removing = ref<Schemas['Passkey'] | null>(null)

async function load() {
  try {
    keys.value = unwrap(await api.GET('/api/v1/me/passkeys')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
onMounted(load)

async function added() {
  toast.success(t('passkeys.added'))
  await load()
  await session.refreshMe()
}

function startRename(k: Schemas['Passkey']) {
  renaming.value = k
  newName.value = k.name
}

async function rename() {
  if (!renaming.value) return
  try {
    unwrap(await api.PATCH('/api/v1/me/passkeys/{passkeyId}', { params: { path: { passkeyId: renaming.value.id } }, body: { name: newName.value } }))
    renaming.value = null
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function remove(password: string) {
  if (!removing.value) return
  unwrap(await api.POST('/api/v1/me/passkeys/{passkeyId}/remove', { params: { path: { passkeyId: removing.value.id } }, body: { password: password || undefined } }))
  toast.success(t('passkeys.removed'))
  removing.value = null
  await load()
}
</script>

<template>
  <Card data-testid="passkeys">
    <CardHeader>
      <CardTitle as="h2">{{ t('passkeys.title') }}</CardTitle>
      <CardDescription>{{ t('passkeys.help') }}</CardDescription>
    </CardHeader>
    <CardContent class="grid grid-cols-1 gap-3">
      <p v-if="!session.passkeysAvailable" class="text-muted-foreground text-sm" data-testid="passkeys-unavailable">{{ t('passkeys.unavailable') }}</p>
      <p v-if="session.passkeysAvailable && keys.length === 0" class="text-muted-foreground text-sm" data-testid="passkeys-none">{{ t('passkeys.none') }}</p>
      <ul v-if="keys.length" class="divide-y rounded-md border">
        <li v-for="k in keys" :key="k.id" class="flex flex-wrap items-center gap-2 p-3 text-sm" data-testid="passkey-item">
          <KeyRound class="text-muted-foreground size-4" aria-hidden="true" />
          <div class="min-w-0 flex-1">
            <p class="truncate font-medium">{{ k.name }}</p>
            <p class="text-muted-foreground">
              {{ t('passkeys.addedAt', { when: fmt.dateTime(k.created_at) }) }} ·
              {{ k.last_used_at ? t('passkeys.lastUsed', { when: fmt.dateTime(k.last_used_at) }) : t('passkeys.neverUsed') }}
            </p>
          </div>
          <Badge v-if="k.synced" variant="secondary">{{ t('passkeys.synced') }}</Badge>
          <Button variant="ghost" size="sm" @click="startRename(k)">{{ t('passkeys.rename') }}</Button>
          <Button variant="ghost" size="sm" data-testid="passkey-remove" @click="removing = k">{{ t('passkeys.remove') }}</Button>
        </li>
      </ul>
      <div v-if="session.passkeysAvailable">
        <Button data-testid="passkey-add" @click="adding = true"><KeyRound aria-hidden="true" />{{ t('passkeys.add') }}</Button>
      </div>
    </CardContent>

    <AddPasskeyDialog v-model:open="adding" @added="added" />
    <Dialog :open="renaming !== null" @update:open="(v) => { if (!v) renaming = null }">
      <DialogContent>
        <form class="grid gap-4" @submit.prevent="rename">
          <DialogHeader><DialogTitle>{{ t('passkeys.rename') }}</DialogTitle></DialogHeader>
          <FormField id="passkey-rename" :label="t('passkeys.name')">
            <Input id="passkey-rename" v-model="newName" maxlength="100" required />
          </FormField>
          <DialogFooter><Button type="submit">{{ t('common.save') }}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
    <PasswordConfirmDialog
      v-if="session.me?.has_password !== false"
      :open="removing !== null"
      :title="t('passkeys.removeTitle', { name: removing?.name ?? '' })"
      :description="t('passkeys.removeHelp')"
      :confirm-label="t('passkeys.remove')"
      destructive
      :on-confirm="remove"
      @update:open="(v) => { if (!v) removing = null }"
    />
    <ConfirmDialog
      v-else
      :open="removing !== null"
      :title="t('passkeys.removeTitle', { name: removing?.name ?? '' })"
      :description="t('passkeys.removeHelp')"
      :confirm-label="t('passkeys.remove')"
      destructive
      :on-confirm="() => remove('')"
      @update:open="(v) => { if (!v) removing = null }"
    />
  </Card>
</template>
