<script setup lang="ts">
import { ref, watch } from 'vue'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'
import { createPasskey, passkeyCancelled } from '@/lib/passkeys'
import { useSessionStore } from '@/stores/session'

/**
 * Adds a passkey: a name to recognize it by, the current password (like other two-factor changes;
 * accounts without one skip it), then the browser's prompt.
 */
const emit = defineEmits<{ added: [Schemas['Passkey']] }>()
const open = defineModel<boolean>('open', { required: true })
const session = useSessionStore()
const name = ref('')
const password = ref('')
const busy = ref(false)
const err = useApiError()

watch(open, (v) => {
  if (v) {
    name.value = ''
    password.value = ''
    err.clear()
  }
})

async function submit() {
  busy.value = true
  err.clear()
  try {
    const ceremony = unwrap(await api.POST('/api/v1/me/passkeys/options', { body: { password: password.value || undefined } }))
    const credential = await createPasskey(ceremony.options)
    const pk = unwrap(await api.POST('/api/v1/me/passkeys', { body: { challenge_token: ceremony.challenge_token, name: name.value, credential } }))
    open.value = false
    emit('added', pk)
  } catch (e) {
    if (!passkeyCancelled(e)) err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <form class="grid gap-4" data-testid="add-passkey" @submit.prevent="submit">
        <DialogHeader>
          <DialogTitle>{{ $t('passkeys.add') }}</DialogTitle>
          <DialogDescription>{{ $t('passkeys.addHelp') }}</DialogDescription>
        </DialogHeader>
        <FormField id="passkey-name" :label="$t('passkeys.name')" :hint="$t('passkeys.nameHint')" :error="err.field('name')">
          <Input id="passkey-name" v-model="name" maxlength="100" data-testid="passkey-name" />
        </FormField>
        <FormField v-if="session.me?.has_password !== false" id="passkey-password" :label="$t('fields.currentPassword')" :error="err.field('password')">
          <Input id="passkey-password" v-model="password" type="password" autocomplete="current-password" required data-testid="passkey-password" />
        </FormField>
        <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
        <DialogFooter>
          <Button type="submit" :disabled="busy" data-testid="passkey-create">{{ $t('passkeys.create') }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
