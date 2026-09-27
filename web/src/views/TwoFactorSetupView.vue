<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { KeyRound } from '@lucide/vue'
import { ref } from 'vue'

import AddPasskeyDialog from '@/components/account/AddPasskeyDialog.vue'
import TotpEnrollment from '@/components/common/TotpEnrollment.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()
const { t } = useI18n()
const addingPasskey = ref(false)

async function done() {
  await session.refreshMe()
  await router.replace({ name: 'home' })
}

async function signOut() {
  await session.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <Card class="w-full max-w-md">
    <CardHeader>
      <CardTitle as="h1" class="text-xl">{{ t('twoFactorSetup.title') }}</CardTitle>
      <CardDescription>{{ t('twoFactorSetup.intro') }}</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-2">
      <template v-if="session.passkeysAvailable">
        <Button variant="outline" data-testid="enroll-passkey" @click="addingPasskey = true"><KeyRound aria-hidden="true" />{{ t('twoFactorSetup.usePasskey') }}</Button>
        <p class="text-muted-foreground text-center text-xs">{{ t('twoFactorSetup.orApp') }}</p>
        <AddPasskeyDialog v-model:open="addingPasskey" @added="done" />
      </template>
      <TotpEnrollment @done="done" />
      <Button variant="link" class="px-0" @click="signOut">{{ t('userMenu.signOut') }}</Button>
    </CardContent>
  </Card>
</template>
