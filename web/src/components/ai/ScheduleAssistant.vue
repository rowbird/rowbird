<script setup lang="ts">
import { Sparkles } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import AIWarnings from '@/components/ai/AIWarnings.vue'
import { useAIStatus } from '@/components/ai/useAIStatus'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'

/**
 * Describes a schedule in words and proposes cron and time zone, with the next runs, applied only
 * when the user asks. Hidden while the assistant is not set up.
 */
const props = defineProps<{ timezone: string }>()
const emit = defineEmits<{ apply: [cron: string, timezone: string] }>()
const { t, locale } = useI18n()
const status = useAIStatus()
const err = useApiError()
const text = ref('')
const busy = ref(false)
const proposal = ref<Schemas['AIProposal'] | null>(null)

onMounted(() => void status.load())

async function suggest() {
  if (!text.value.trim() || busy.value) return
  busy.value = true
  err.clear()
  try {
    proposal.value = unwrap(await api.POST('/api/v1/ai/generate', { body: { task: 'schedule', prompt: text.value, timezone: props.timezone || undefined } }))
  } catch (e) {
    err.error.value = e
    proposal.value = null
  } finally {
    busy.value = false
  }
}

function apply() {
  const s = proposal.value?.schedule
  if (!s) return
  emit('apply', s.cron, s.timezone)
  proposal.value = null
  text.value = ''
}

function when(iso: string, tz: string) {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'full', timeStyle: 'short', timeZone: tz }).format(new Date(iso))
}
</script>

<template>
  <div v-if="status.enabled.value" class="grid gap-2 rounded-md border border-dashed p-3" data-testid="schedule-assistant">
    <form class="flex flex-wrap items-end gap-2" @submit.prevent="suggest">
      <div class="grid min-w-60 flex-1 gap-1">
        <label for="schedule-ai-text" class="text-sm font-medium">{{ t('ai.schedule.label') }}</label>
        <Input id="schedule-ai-text" v-model="text" maxlength="2000" :placeholder="t('ai.schedule.placeholder')" data-testid="schedule-ai-text" />
      </div>
      <Button type="submit" variant="outline" :disabled="busy || !text.trim()" data-testid="schedule-ai-suggest">
        <Sparkles aria-hidden="true" />{{ busy ? t('ai.panel.generating') : t('ai.schedule.suggest') }}
      </Button>
    </form>
    <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
    <div v-if="proposal" class="grid gap-2 text-sm" data-testid="schedule-ai-proposal">
      <p v-if="proposal.explanation">{{ proposal.explanation }}</p>
      <AIWarnings :warnings="proposal.warnings" />
      <template v-if="proposal.schedule">
        <p>
          <span class="font-medium" data-testid="schedule-ai-description">{{ proposal.schedule.description }}</span>
          <span class="text-muted-foreground ml-2 font-mono text-xs">{{ proposal.schedule.cron }} · {{ proposal.schedule.timezone }}</span>
        </p>
        <ol class="text-muted-foreground grid gap-0.5 text-xs">
          <li v-for="n in proposal.schedule.next" :key="n">{{ when(n, proposal.schedule.timezone) }}</li>
        </ol>
        <div class="flex gap-2">
          <Button type="button" size="sm" data-testid="schedule-ai-apply" @click="apply">{{ t('ai.panel.apply') }}</Button>
          <Button type="button" size="sm" variant="ghost" @click="proposal = null">{{ t('ai.panel.discard') }}</Button>
        </div>
      </template>
    </div>
  </div>
</template>
