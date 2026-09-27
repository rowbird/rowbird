<script setup lang="ts">
import { File, Link } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'

/**
 * A delivery's rendered message. HTML bodies are shown in a frame without scripts, same-origin
 * access or navigation, since they carry database content.
 */
defineProps<{ preview: Schemas['DeliveryPreview'] }>()
const { t } = useI18n()
</script>

<template>
  <div class="grid gap-3 text-sm" data-testid="delivery-preview">
    <div v-if="preview.subject" class="grid gap-1">
      <span class="text-muted-foreground text-xs">{{ t('deliveries.preview.subject') }}</span>
      <span class="font-medium" data-testid="preview-subject">{{ preview.subject }}</span>
    </div>
    <iframe
      v-if="preview.body_type === 'html'"
      :srcdoc="preview.body"
      sandbox=""
      referrerpolicy="no-referrer"
      class="h-96 w-full rounded-md border bg-white"
      :title="t('deliveries.preview.title')"
      data-testid="preview-html"
    />
    <pre v-else class="bg-muted max-h-96 overflow-auto rounded-md p-3 font-mono text-xs whitespace-pre-wrap" data-testid="preview-text">{{ preview.body }}</pre>
    <ul v-if="preview.attachments.length || preview.links.length" class="grid gap-1">
      <li v-for="a in preview.attachments" :key="a" class="flex items-center gap-2"><File class="size-4" aria-hidden="true" />{{ a }}</li>
      <li v-for="l in preview.links" :key="l" class="flex items-center gap-2"><Link class="size-4" aria-hidden="true" />{{ l }}</li>
    </ul>
  </div>
</template>
