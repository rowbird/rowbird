<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'

/** Chooses a new password with the emailed link, then sends the user to sign in. */
const { t } = useI18n()
const route = useRoute()
const err = useApiError()
const token = computed(() => (typeof route.query.token === 'string' ? route.query.token : ''))
const form = reactive({ next: '', confirm: '' })
const mismatch = ref(false)
const done = ref(false)
const busy = ref(false)
const invalid = computed(() => !token.value || (err.error.value instanceof ApiError && err.error.value.code === 'auth.reset_invalid'))

async function submit() {
  mismatch.value = form.next !== form.confirm
  if (mismatch.value) return
  busy.value = true
  err.clear()
  try {
    unwrap(await api.POST('/api/v1/auth/password-reset/confirm', { body: { token: token.value, password: form.next } }))
    done.value = true
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="w-full max-w-sm">
    <CardHeader>
      <CardTitle as="h1" class="text-xl">{{ t('resetPassword.title') }}</CardTitle>
      <CardDescription>{{ t('resetPassword.help') }}</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <template v-if="done">
        <Alert data-testid="reset-done"><AlertDescription>{{ t('resetPassword.done') }}</AlertDescription></Alert>
        <Button as-child><RouterLink to="/login" data-testid="reset-login">{{ t('resetPassword.signIn') }}</RouterLink></Button>
      </template>
      <template v-else-if="invalid">
        <Alert variant="destructive" data-testid="reset-invalid"><AlertDescription>{{ t('errors.auth.reset_invalid') }}</AlertDescription></Alert>
        <RouterLink to="/forgot-password" class="text-sm underline">{{ t('resetPassword.again') }}</RouterLink>
      </template>
      <form v-else class="grid gap-4" @submit.prevent="submit">
        <FormField id="reset-password" :label="t('fields.newPassword')" :hint="t('fields.passwordHint')" :error="err.field('password')">
          <Input id="reset-password" v-model="form.next" type="password" autocomplete="new-password" required autofocus data-testid="reset-password" />
        </FormField>
        <FormField id="reset-confirm" :label="t('fields.confirmPassword')" :error="mismatch ? t('fields.passwordMismatch') : undefined">
          <Input id="reset-confirm" v-model="form.confirm" type="password" autocomplete="new-password" required data-testid="reset-confirm" />
        </FormField>
        <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
        <Button type="submit" :disabled="busy" data-testid="reset-submit">{{ t('resetPassword.submit') }}</Button>
      </form>
    </CardContent>
  </Card>
</template>
