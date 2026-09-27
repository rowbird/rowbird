<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import ChangePasswordForm from '@/components/account/ChangePasswordForm.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()
const { t } = useI18n()
const signingOut = ref(false)

async function done() {
  await session.refreshMe()
  await router.replace({ name: 'home' })
}

async function signOut() {
  signingOut.value = true
  await session.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <Card class="w-full max-w-sm">
    <CardHeader>
      <CardTitle as="h1" class="text-xl">{{ t('changePassword.title') }}</CardTitle>
      <CardDescription>{{ t('changePassword.intro') }}</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-2">
      <ChangePasswordForm @changed="done" />
      <Button variant="link" class="px-0" :disabled="signingOut" @click="signOut">{{ t('userMenu.signOut') }}</Button>
    </CardContent>
  </Card>
</template>
