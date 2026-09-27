<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'

import FormField from './FormField.vue'
import RecoveryCodes from './RecoveryCodes.vue'

const emit = defineEmits<{ done: [] }>()

const setup = ref<Schemas['TotpSetup'] | null>(null)
const code = ref('')
const codes = ref<string[] | null>(null)
const busy = ref(false)
const err = useApiError()

onMounted(async () => {
  try {
    setup.value = unwrap(await api.POST('/api/v1/me/2fa/totp/setup'))
  } catch (e) {
    err.error.value = e
  }
})

async function confirm() {
  busy.value = true
  err.clear()
  try {
    codes.value = unwrap(await api.POST('/api/v1/me/2fa/totp/confirm', { body: { code: code.value } })).codes
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="grid gap-4">
    <template v-if="codes">
      <RecoveryCodes :codes="codes" />
      <Button data-testid="totp-finish" @click="emit('done')">{{ $t('twoFactor.savedCodes') }}</Button>
    </template>
    <template v-else-if="setup">
      <p class="text-sm">{{ $t('twoFactor.scan') }}</p>
      <img :src="setup.qr_code" :alt="$t('twoFactor.qrAlt')" class="mx-auto size-48 rounded bg-white p-2">
      <details class="text-sm">
        <summary class="cursor-pointer">{{ $t('twoFactor.manualEntry') }}</summary>
        <code class="bg-muted mt-2 block rounded p-2 font-mono break-all" data-testid="totp-secret">{{ setup.secret }}</code>
      </details>
      <form class="grid gap-3" @submit.prevent="confirm">
        <FormField id="totp-code" :label="$t('twoFactor.code')" :error="err.field('code')">
          <Input id="totp-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" maxlength="6" required />
        </FormField>
        <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm">{{ err.message.value }}</p>
        <Button type="submit" :disabled="busy || code.length < 6">{{ $t('twoFactor.enable') }}</Button>
      </form>
    </template>
    <p v-else-if="err.message.value" class="text-destructive text-sm">{{ err.message.value }}</p>
  </div>
</template>
