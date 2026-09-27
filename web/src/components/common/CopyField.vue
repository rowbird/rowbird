<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { ref } from 'vue'

import { Button } from '@/components/ui/button'

const props = defineProps<{ value: string; label: string }>()
const copied = ref(false)

async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <div class="bg-muted flex items-center gap-2 rounded-md border p-2">
    <code class="flex-1 font-mono text-sm break-all select-all" data-testid="copy-value">{{ value }}</code>
    <Button type="button" variant="ghost" size="icon" :aria-label="copied ? $t('common.copied') : `${$t('common.copy')}: ${label}`" @click="copy">
      <Check v-if="copied" aria-hidden="true" />
      <Copy v-else aria-hidden="true" />
    </Button>
  </div>
</template>
