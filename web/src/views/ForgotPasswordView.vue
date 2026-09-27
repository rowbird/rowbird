<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'

/** Asks for a reset link. The answer is the same whether or not the email has an account. */
const { t } = useI18n()
const err = useApiError()
const email = ref('')
const sent = ref(false)
const busy = ref(false)

async function submit() {
  busy.value = true
  err.clear()
  try {
    unwrap(await api.POST('/api/v1/auth/password-reset', { body: { email: email.value } }))
    sent.value = true
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
      <CardTitle as="h1" class="text-xl">{{ t('forgotPassword.title') }}</CardTitle>
      <CardDescription>{{ t('forgotPassword.help') }}</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <Alert v-if="sent" data-testid="forgot-sent">
        <AlertDescription>{{ t('forgotPassword.sent', { email }) }}</AlertDescription>
      </Alert>
      <form v-else class="grid gap-4" @submit.prevent="submit">
        <FormField id="forgot-email" :label="t('fields.email')">
          <Input id="forgot-email" v-model="email" type="email" autocomplete="username" required autofocus data-testid="forgot-email" />
        </FormField>
        <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
        <Button type="submit" :disabled="busy" data-testid="forgot-submit">{{ t('forgotPassword.submit') }}</Button>
      </form>
      <RouterLink to="/login" class="text-sm underline">{{ t('forgotPassword.back') }}</RouterLink>
    </CardContent>
  </Card>
</template>
