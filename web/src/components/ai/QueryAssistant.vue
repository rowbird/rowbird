<script setup lang="ts">
import { Sparkles } from '@lucide/vue'
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import AIWarnings from '@/components/ai/AIWarnings.vue'
import { useAIStatus } from '@/components/ai/useAIStatus'
import DiffView from '@/components/query/DiffView.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useApiError } from '@/composables/useApiError'
import { browserTimeZone } from '@/lib/timezones'
import { useSessionStore } from '@/stores/session'

/**
 * The AI panel of the query editor (Ctrl/Cmd+I): the user describes the query, the proposal shows as
 * a diff against the current SQL, and nothing changes until Apply. Nothing ever runs by itself.
 */
const props = defineProps<{ connectionId: string; sql: string }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ apply: [proposal: Schemas['AIProposal']] }>()

const { t } = useI18n()
const session = useSessionStore()
const status = useAIStatus()
const err = useApiError()
const prompt = ref('')
const busy = ref(false)
const proposal = ref<Schemas['AIProposal'] | null>(null)
/** The SQL the proposal was made against, for the diff. */
const base = ref('')

watch(open, (v) => {
  if (v) void status.load()
})

async function generate() {
  if (!prompt.value.trim() || busy.value) return
  busy.value = true
  err.clear()
  try {
    base.value = props.sql
    proposal.value = unwrap(await api.POST('/api/v1/ai/generate', {
      body: { task: 'query', prompt: prompt.value, connection_id: props.connectionId, sql: props.sql || undefined, timezone: browserTimeZone() },
    }))
  } catch (e) {
    err.error.value = e
    proposal.value = null
  } finally {
    busy.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault()
    void generate()
  }
}

function apply() {
  if (!proposal.value) return
  emit('apply', proposal.value)
  proposal.value = null
  prompt.value = ''
  open.value = false
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent side="right" class="w-full overflow-y-auto sm:max-w-2xl" data-testid="ai-panel">
      <SheetHeader>
        <SheetTitle class="flex items-center gap-2"><Sparkles class="size-4" aria-hidden="true" />{{ t('ai.panel.title') }}</SheetTitle>
        <SheetDescription>{{ t('ai.panel.help') }}</SheetDescription>
      </SheetHeader>
      <div class="grid gap-4 px-4 pb-4">
        <div v-if="status.enabled.value === false" class="grid gap-2 text-sm" data-testid="ai-off">
          <p>{{ t('ai.panel.off') }}</p>
          <RouterLink v-if="session.hasRole('admin')" to="/settings/ai" class="underline underline-offset-2">{{ t('ai.panel.setUp') }}</RouterLink>
          <p v-else class="text-muted-foreground">{{ t('ai.panel.askAdmin') }}</p>
        </div>
        <form v-else class="grid gap-2" @submit.prevent="generate">
          <label for="ai-prompt" class="text-sm font-medium">{{ t('ai.panel.prompt') }}</label>
          <textarea
            id="ai-prompt"
            v-model="prompt"
            rows="3"
            maxlength="2000"
            :placeholder="t('ai.panel.placeholder')"
            class="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 text-sm shadow-xs outline-none focus-visible:ring-[3px]"
            data-testid="ai-prompt"
            @keydown="onKey"
          />
          <p class="text-muted-foreground text-xs">{{ t('ai.panel.privacy') }}</p>
          <div>
            <Button type="submit" :disabled="busy || !prompt.trim() || !connectionId" data-testid="ai-generate">
              <Sparkles aria-hidden="true" />{{ busy ? t('ai.panel.generating') : t('ai.panel.generate') }}
            </Button>
          </div>
          <p v-if="err.message.value" class="text-destructive text-sm" role="alert" data-testid="ai-error">{{ err.message.value }}</p>
        </form>

        <section v-if="proposal" class="grid gap-3" data-testid="ai-proposal">
          <h3 class="text-sm font-semibold">{{ t('ai.panel.proposal') }}</h3>
          <p v-if="proposal.suggested_name" class="text-sm"><span class="text-muted-foreground">{{ t('ai.panel.name') }}:</span> {{ proposal.suggested_name }}</p>
          <p class="text-sm whitespace-pre-line" data-testid="ai-explanation">{{ proposal.explanation }}</p>
          <AIWarnings :warnings="proposal.warnings" />
          <div class="overflow-hidden rounded-md border" data-testid="ai-diff">
            <DiffView :original="base" :modified="proposal.sql" />
          </div>
          <div v-if="proposal.params.length" class="flex flex-wrap items-center gap-1 text-xs">
            <span class="text-muted-foreground">{{ t('ai.panel.params') }}</span>
            <Badge v-for="p in proposal.params" :key="p.name" variant="outline" class="font-mono">{{ p.name }}</Badge>
          </div>
          <p v-if="proposal.schedule" class="text-muted-foreground text-xs" data-testid="ai-schedule-note">
            {{ t('ai.panel.scheduleNote', { schedule: proposal.schedule.description }) }}
          </p>
          <div class="flex gap-2">
            <Button data-testid="ai-apply" @click="apply">{{ t('ai.panel.apply') }}</Button>
            <Button variant="ghost" @click="proposal = null">{{ t('ai.panel.discard') }}</Button>
          </div>
          <p class="text-muted-foreground text-xs">{{ t('ai.panel.applyNote') }}</p>
        </section>
      </div>
    </SheetContent>
  </Sheet>
</template>
