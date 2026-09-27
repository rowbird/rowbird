/** The JSON Schema subset plugins declare (docs/spec/04-plugins.md), with Rowbird's x- extensions. */
export interface FieldSchema {
  type: 'string' | 'integer' | 'number' | 'boolean'
  default?: unknown
  enum?: string[]
  minimum?: number
  maximum?: number
  minLength?: number
  maxLength?: number
  format?: string
  writeOnly?: boolean
  'x-secret'?: boolean
  'x-group'?: string
  'x-multiline'?: boolean
  'x-show-if'?: { field: string; in: unknown[] }
  'x-label': string
  'x-help'?: string
}

export interface ConfigSchema {
  type: 'object'
  properties: Record<string, FieldSchema>
  required: string[]
  'x-order': string[]
}

export interface Field extends FieldSchema {
  key: string
  required: boolean
}

export type ConfigValues = Record<string, unknown>

/** A stored secret as the API returns it; sending it back keeps the value. */
export interface SecretPlaceholder {
  configured: boolean
}

export function isPlaceholder(v: unknown): v is SecretPlaceholder {
  return typeof v === 'object' && v !== null && 'configured' in v
}

export function fields(schema: ConfigSchema): Field[] {
  return schema['x-order'].map((key) => ({ ...schema.properties[key]!, key, required: schema.required.includes(key) }))
}

/** Value of a field, falling back to its default. */
export function valueOf(schema: ConfigSchema, values: ConfigValues, key: string): unknown {
  return key in values && values[key] !== undefined ? values[key] : schema.properties[key]?.default
}

/** Whether a field applies, following x-show-if chains. */
export function isVisible(schema: ConfigSchema, values: ConfigValues, key: string): boolean {
  const cond = schema.properties[key]?.['x-show-if']
  if (!cond) return true
  if (!isVisible(schema, values, cond.field)) return false
  return cond.in.includes(valueOf(schema, values, cond.field))
}

/** Fields grouped by x-group, keeping the order in which groups first appear. */
export function groups(schema: ConfigSchema): { name: string; fields: Field[] }[] {
  const out: { name: string; fields: Field[] }[] = []
  for (const f of fields(schema)) {
    const name = f['x-group'] ?? 'connection'
    let g = out.find((x) => x.name === name)
    if (!g) out.push((g = { name, fields: [] }))
    g.fields.push(f)
  }
  return out
}

/** Initial values: defaults for every field, placeholders kept as they come from the API. */
export function applyDefaults(schema: ConfigSchema, values: ConfigValues = {}): ConfigValues {
  const out: ConfigValues = { ...values }
  for (const f of fields(schema)) {
    if (!(f.key in out) && f.default !== undefined) out[f.key] = f.default
  }
  return out
}

/**
 * The config to send: only visible fields, empty optional strings dropped, placeholders of
 * untouched secrets kept so the server keeps the stored value.
 */
export function toPayload(schema: ConfigSchema, values: ConfigValues): ConfigValues {
  const out: ConfigValues = {}
  for (const f of fields(schema)) {
    if (!isVisible(schema, values, f.key)) continue
    const v = values[f.key]
    if (v === undefined || v === '') continue
    if (isPlaceholder(v)) {
      if (v.configured) out[f.key] = v
      continue
    }
    out[f.key] = v
  }
  return out
}

/** Nested message tree from flat "a.b.c" keys, as vue-i18n expects. */
export function unflatten(flat: Record<string, string>): Record<string, unknown> {
  const root: Record<string, unknown> = {}
  for (const [key, text] of Object.entries(flat)) {
    const parts = key.split('.')
    let node = root
    for (const part of parts.slice(0, -1)) {
      if (typeof node[part] !== 'object' || node[part] === null) node[part] = {}
      node = node[part] as Record<string, unknown>
    }
    node[parts[parts.length - 1]!] = text
  }
  return root
}

/**
 * The delivery options that matter for system alerts: message templates are left out, because an
 * alert carries its own text.
 */
export function alertOptionSchema(schema: ConfigSchema): ConfigSchema {
  const keep = schema['x-order'].filter((k) => schema.properties[k]?.['x-help'] !== 'plugin.template.help')
  return {
    ...schema,
    'x-order': keep,
    properties: Object.fromEntries(keep.map((k) => [k, schema.properties[k]!])),
    required: schema.required.filter((k) => keep.includes(k)),
  }
}

/** Variables of message templates (docs/spec/04-plugins.md, "Message templates"). */
export const TEMPLATE_VARIABLES = [
  'report.name',
  'report.url',
  'run.status',
  'run.rows',
  'run.date',
  'run.started_at',
  'run.duration',
  'run.url',
  'link.url',
  'link.expires_at',
  'condition.summary',
  'format',
] as const
