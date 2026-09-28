<script setup lang="ts">
import { ExternalLink } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useFormat } from '@/composables/useFormat'
import { noteAbout } from '@/composables/useNewVersion'
import { useSessionStore } from '@/stores/session'

/** About (docs/spec/06-ui.md, "Settings"): the version, and the optional check for new releases. */
const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const canEdit = computed(() => session.hasRole('admin'))
const about = ref<Schemas['About'] | null>(null)

async function load() {
  about.value = unwrap(await api.GET('/api/v1/system/about'))
  noteAbout(about.value)
}

onMounted(async () => {
  try {
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

async function setCheck(value: boolean) {
  try {
    unwrap(await api.PATCH('/api/v1/settings', { body: { update_check: value } }))
    await load()
    toast.success(t('settings.saved'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
</script>

<template>
  <div v-if="about" class="grid gap-6" data-testid="about">
    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.about.title') }}</CardTitle>
        <CardDescription>{{ t('app.tagline') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
          <dt class="text-muted-foreground">{{ t('settings.about.version') }}</dt>
          <dd class="font-mono" data-testid="about-version">{{ about.version }}</dd>
          <dt class="text-muted-foreground">{{ t('settings.about.commit') }}</dt>
          <dd class="font-mono">{{ about.commit }}</dd>
          <dt class="text-muted-foreground">{{ t('settings.about.built') }}</dt>
          <dd>{{ about.build_date }}</dd>
          <dt class="text-muted-foreground">{{ t('settings.about.platform') }}</dt>
          <dd>{{ about.platform }} · {{ about.go_version }}</dd>
        </dl>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.about.updatesTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.about.updatesHelp') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-4">
        <p v-if="!about.update_check_allowed" class="text-sm" data-testid="update-check-disabled">{{ t('settings.about.disabledByServer') }}</p>
        <template v-else>
          <div class="flex items-center gap-3">
            <Switch id="update-check" :model-value="about.update_check_enabled" :disabled="!canEdit" data-testid="update-check" @update:model-value="setCheck" />
            <Label for="update-check">{{ t('settings.about.checkForUpdates') }}</Label>
          </div>
          <Alert v-if="about.update_available" data-testid="update-available">
            <AlertDescription class="flex flex-wrap items-center gap-2">
              {{ t('settings.about.available', { version: about.latest_version }) }}
              <a v-if="about.release_url" :href="about.release_url" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 underline">
                {{ t('settings.about.releaseNotes') }}<ExternalLink class="size-3" aria-hidden="true" />
              </a>
            </AlertDescription>
          </Alert>
          <p v-else-if="about.update_check_enabled && about.checked_at" class="text-muted-foreground text-sm">
            {{ t('settings.about.upToDate', { when: fmt.dateTime(about.checked_at) }) }}
          </p>
        </template>
      </CardContent>
    </Card>
  </div>
</template>
