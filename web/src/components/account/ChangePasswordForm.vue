<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'

const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const err = useApiError()
const form = reactive({ current: '', next: '', confirm: '' })
const mismatch = ref(false)
const busy = ref(false)

async function submit() {
  mismatch.value = form.next !== form.confirm
  if (mismatch.value) return
  busy.value = true
  err.clear()
  try {
    unwrap(await api.POST('/api/v1/me/password', { body: { current_password: form.current, new_password: form.next } }))
    form.current = form.next = form.confirm = ''
    emit('changed')
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="grid gap-4" @submit.prevent="submit">
    <FormField id="current-password" :label="t('fields.currentPassword')" :error="err.field('current_password')">
      <Input id="current-password" v-model="form.current" type="password" autocomplete="current-password" required />
    </FormField>
    <FormField id="new-password" :label="t('fields.newPassword')" :hint="t('fields.passwordHint')" :error="err.field('new_password')">
      <Input id="new-password" v-model="form.next" type="password" autocomplete="new-password" required />
    </FormField>
    <FormField id="confirm-password" :label="t('fields.confirmPassword')" :error="mismatch ? t('fields.passwordMismatch') : undefined">
      <Input id="confirm-password" v-model="form.confirm" type="password" autocomplete="new-password" required />
    </FormField>
    <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
    <Button type="submit" :disabled="busy" data-testid="change-password-submit">{{ t('changePassword.submit') }}</Button>
  </form>
</template>
