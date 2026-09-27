<script setup lang="ts">
import { Download } from '@lucide/vue'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import CopyField from './CopyField.vue'

const props = defineProps<{ codes: string[] }>()

function download() {
  const blob = new Blob([props.codes.join('\n') + '\n'], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'rowbird-recovery-codes.txt'
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <div class="grid gap-3">
    <Alert>
      <AlertTitle>{{ $t('twoFactor.codesTitle') }}</AlertTitle>
      <AlertDescription>{{ $t('twoFactor.codesHelp') }}</AlertDescription>
    </Alert>
    <ul class="grid grid-cols-2 gap-2 font-mono text-sm" data-testid="recovery-codes">
      <li v-for="c in codes" :key="c" class="bg-muted rounded px-2 py-1 text-center">{{ c }}</li>
    </ul>
    <div class="flex flex-wrap gap-2">
      <CopyField class="flex-1" :value="codes.join(' ')" :label="$t('twoFactor.codesTitle')" />
      <Button type="button" variant="outline" @click="download">
        <Download aria-hidden="true" />
        {{ $t('common.download') }}
      </Button>
    </div>
  </div>
</template>
