<script setup lang="ts">
import { Copy } from '@lucide/vue'
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useApiError } from '@/composables/useApiError'

/**
 * Single sign-on (docs/spec/06-ui.md, "Settings > Security"): the provider, the client, and who
 * may sign in. The client secret is only ever shown as saved.
 */
const { t, te } = useI18n()
const err = useApiError()
const loaded = ref(false)
const busy = ref(false)
const saved = ref<Schemas['OIDCSettings'] | null>(null)
const form = reactive({
  enabled: false, issuer: '', client_id: '', client_secret: '', scopes: '', button_label: '',
  auto_provision: false, default_role: 'viewer' as 'viewer' | 'editor', allowed_domains: '',
})
const replaceSecret = ref(false)
const testResult = ref<Schemas['OIDCTestResult'] | null>(null)
const roleOptions = (['viewer', 'editor'] as const).map((r) => ({ value: r, label: t(`roles.${r}`) }))
const list = (s: string) => s.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean)

function fill(s: Schemas['OIDCSettings']) {
  saved.value = s
  Object.assign(form, {
    enabled: s.enabled, issuer: s.issuer, client_id: s.client_id, client_secret: '', scopes: s.scopes.join(' '),
    button_label: s.button_label, auto_provision: s.auto_provision, default_role: s.default_role, allowed_domains: s.allowed_domains.join(', '),
  })
  replaceSecret.value = !s.client_secret_configured
}

onMounted(async () => {
  try {
    fill(unwrap(await api.GET('/api/v1/settings/oidc')))
    loaded.value = true
  } catch (e) {
    err.error.value = e
  }
})

async function save() {
  busy.value = true
  err.clear()
  try {
    const body: Schemas['OIDCSettingsInput'] = {
      enabled: form.enabled, issuer: form.issuer, client_id: form.client_id, scopes: list(form.scopes), button_label: form.button_label,
      auto_provision: form.auto_provision, default_role: form.default_role, allowed_domains: list(form.allowed_domains),
    }
    if (replaceSecret.value) body.client_secret = form.client_secret
    fill(unwrap(await api.PUT('/api/v1/settings/oidc', { body })))
    toast.success(t('settings.saved'))
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}

async function test() {
  testResult.value = null
  try {
    testResult.value = unwrap(await api.POST('/api/v1/settings/oidc/test', { body: { issuer: form.issuer } }))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function copyRedirect() {
  if (!saved.value?.redirect_uri) return
  await navigator.clipboard.writeText(saved.value.redirect_uri)
  toast.success(t('common.copied'))
}
</script>

<template>
  <Card data-testid="oidc-settings">
    <CardHeader>
      <CardTitle as="h2">{{ t('settings.security.oidc.title') }}</CardTitle>
      <CardDescription>{{ t('settings.security.oidc.help') }}</CardDescription>
    </CardHeader>
    <CardContent>
      <Alert v-if="saved && !saved.available" class="mb-4" data-testid="oidc-unavailable">
        <AlertDescription>{{ t('settings.security.oidc.needsBaseURL') }}</AlertDescription>
      </Alert>
      <form v-if="loaded" class="grid gap-4 sm:max-w-xl" @submit.prevent="save">
        <div class="flex items-center gap-3">
          <Switch id="oidc-enabled" v-model="form.enabled" data-testid="oidc-enabled" />
          <Label for="oidc-enabled">{{ t('settings.security.oidc.enabled') }}</Label>
        </div>
        <FormField v-if="saved?.redirect_uri" id="oidc-redirect" :label="t('settings.security.oidc.redirectURI')" :hint="t('settings.security.oidc.redirectHint')">
          <div class="flex gap-2">
            <Input id="oidc-redirect" :model-value="saved.redirect_uri" readonly class="font-mono text-xs" data-testid="oidc-redirect-uri" />
            <Button type="button" variant="outline" size="icon" :aria-label="t('common.copy')" @click="copyRedirect"><Copy aria-hidden="true" /></Button>
          </div>
        </FormField>
        <FormField id="oidc-issuer" :label="t('settings.security.oidc.issuer')" :hint="t('settings.security.oidc.issuerHint')" :error="err.field('issuer')">
          <div class="flex gap-2">
            <Input id="oidc-issuer" v-model="form.issuer" placeholder="https://accounts.example.com" data-testid="oidc-issuer" />
            <Button type="button" variant="outline" :disabled="!form.issuer" data-testid="oidc-test" @click="test">{{ t('settings.security.oidc.test') }}</Button>
          </div>
        </FormField>
        <p v-if="testResult" class="text-sm" :class="testResult.ok ? 'text-emerald-600' : 'text-destructive'" data-testid="oidc-test-result">
          {{ testResult.ok ? t('settings.security.oidc.testOk') : t(`errors.${testResult.error_code ?? 'auth.oidc_discovery_failed'}`) }}
        </p>
        <FormField id="oidc-client-id" :label="t('settings.security.oidc.clientId')" :error="err.field('client_id')">
          <Input id="oidc-client-id" v-model="form.client_id" autocomplete="off" data-testid="oidc-client-id" />
        </FormField>
        <FormField id="oidc-client-secret" :label="t('settings.security.oidc.clientSecret')" :hint="t('settings.security.oidc.clientSecretHint')">
          <div v-if="!replaceSecret" class="flex items-center gap-2 text-sm">
            <span data-testid="oidc-secret-configured">{{ t('settings.security.oidc.secretSaved') }}</span>
            <Button type="button" variant="link" class="px-0" @click="replaceSecret = true">{{ t('settings.security.oidc.replace') }}</Button>
          </div>
          <Input v-else id="oidc-client-secret" v-model="form.client_secret" type="password" autocomplete="new-password" data-testid="oidc-client-secret" />
        </FormField>
        <FormField id="oidc-scopes" :label="t('settings.security.oidc.scopes')" :hint="t('settings.security.oidc.scopesHint')">
          <Input id="oidc-scopes" v-model="form.scopes" placeholder="openid email profile" />
        </FormField>
        <FormField id="oidc-label" :label="t('settings.security.oidc.buttonLabel')" :hint="t('settings.security.oidc.buttonLabelHint')" :error="err.field('button_label')">
          <Input id="oidc-label" v-model="form.button_label" maxlength="60" data-testid="oidc-label" />
        </FormField>
        <FormField id="oidc-domains" :label="t('settings.security.oidc.domains')" :hint="t('settings.security.oidc.domainsHint')" :error="err.field('allowed_domains')">
          <Input id="oidc-domains" v-model="form.allowed_domains" placeholder="example.com" />
        </FormField>
        <div class="flex items-center gap-3">
          <Switch id="oidc-provision" v-model="form.auto_provision" data-testid="oidc-provision" />
          <Label for="oidc-provision">{{ t('settings.security.oidc.autoProvision') }}</Label>
        </div>
        <FormField v-if="form.auto_provision" id="oidc-role" :label="t('settings.security.oidc.defaultRole')" :error="err.field('default_role')">
          <NativeSelect id="oidc-role" v-model="form.default_role" :options="roleOptions" />
        </FormField>
        <p class="text-muted-foreground text-sm">{{ t('settings.security.oidc.linking') }}</p>
        <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
        <div><Button type="submit" :disabled="busy" data-testid="oidc-save">{{ t('common.save') }}</Button></div>
      </form>
    </CardContent>
  </Card>
</template>
