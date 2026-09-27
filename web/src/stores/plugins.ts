import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { ConfigSchema } from '@/lib/schema'
import { unflatten } from '@/lib/schema'

export type PluginInfo = Omit<Schemas['PluginInfo'], 'schema' | 'delivery_schema'> & { schema: ConfigSchema; delivery_schema?: ConfigSchema }

/** What a destination can do with a given channel's settings (GET /channels returns them). */
export interface DestinationCapabilities {
  supports_attachments: boolean
  max_attachment_bytes?: number
  max_text_chars?: number
  inline_target?: string
  supports_status: boolean
  always_notify: boolean
  modes: Schemas['DeliveryMode'][]
}

export interface ConnectorCapabilities {
  read_only_tx: boolean
  server_timeout: boolean
  cancel: boolean
  multi_statement: boolean
  placeholder_style: string
  dialect: string
  network: boolean
}

/**
 * Plugin texts are shown as written: template help mentions Mustache variables such as
 * {{report.slug}}, and recipients help may show addresses. vue-i18n would read braces, "@", "$"
 * and "|" as its own syntax, so each one becomes a literal.
 */
export function literalMessages(messages: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(messages).map(([k, v]) => [k, v.replace(/[{}@$|]/g, (c) => `{'${c}'}`)]))
}

/** The plugin catalog; plugin translations are merged into vue-i18n when it loads. */
export const usePluginsStore = defineStore('plugins', () => {
  const plugins = ref<PluginInfo[]>([])
  const loaded = ref(false)
  const { mergeLocaleMessage } = useI18n({ useScope: 'global' })
  let loading: Promise<void> | null = null

  function load() {
    if (loaded.value) return Promise.resolve()
    loading ??= (async () => {
      try {
        const cat = unwrap(await api.GET('/api/v1/plugins'))
        for (const [locale, messages] of Object.entries(cat.messages)) {
          mergeLocaleMessage(locale, unflatten(literalMessages(messages)))
        }
        plugins.value = cat.plugins as unknown as PluginInfo[]
        loaded.value = true
      } finally {
        loading = null
      }
    })()
    return loading
  }

  const connectors = computed(() => plugins.value.filter((p) => p.kind === 'connector'))

  function connector(id: string) {
    return connectors.value.find((p) => p.id === id)
  }

  const destinations = computed(() => plugins.value.filter((p) => p.kind === 'destination'))

  function destination(id: string) {
    return destinations.value.find((p) => p.id === id)
  }

  /** Formatters that produce files (csv, xlsx, json, pdf), as opposed to inline renderings. */
  const fileFormatters = computed(() => plugins.value.filter((p) => p.kind === 'formatter' && (p.capabilities as { kind?: string }).kind === 'file'))

  const aiProviders = computed(() => plugins.value.filter((p) => p.kind === 'ai_provider'))

  function aiProvider(id: string) {
    return aiProviders.value.find((p) => p.id === id)
  }

  return { plugins, loaded, connectors, connector, destinations, destination, fileFormatters, aiProviders, aiProvider, load }
})
