// Build-time loader for the API endpoint list: reads api/openapi.yaml, the source of truth of the
// HTTP API (ADR-0005), so this page never drifts from the server.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { parse } from 'yaml'

const specPath = fileURLToPath(new URL('../../../api/openapi.yaml', import.meta.url))

export interface Authz {
  role?: string
  scope?: string
  session_only?: boolean
  allow_restricted?: boolean
}

export interface Operation {
  method: string
  path: string
  summary: string
  description: string
  public: boolean
  authz?: Authz
}

export interface Tag {
  name: string
  description: string
  operations: Operation[]
}

const methods = ['get', 'post', 'put', 'patch', 'delete'] as const

export default {
  watch: [specPath],
  load(): Tag[] {
    const spec = parse(readFileSync(specPath, 'utf8'))
    const tags: Tag[] = (spec.tags ?? []).map((t: { name: string; description?: string }) => ({
      name: t.name,
      description: t.description ?? '',
      operations: [],
    }))
    const byName = new Map(tags.map((t) => [t.name, t]))
    for (const [path, item] of Object.entries<Record<string, any>>(spec.paths ?? {})) {
      for (const method of methods) {
        const op = item[method]
        if (!op) continue
        const tag = byName.get(op.tags?.[0]) ?? byName.get('other')
        const entry: Operation = {
          method: method.toUpperCase(),
          path,
          summary: op.summary ?? '',
          description: op.description ?? '',
          public: Array.isArray(op.security) && op.security.length === 0,
          authz: op['x-rowbird-authz'],
        }
        if (tag) {
          tag.operations.push(entry)
        } else {
          const other: Tag = { name: 'other', description: '', operations: [entry] }
          tags.push(other)
          byName.set('other', other)
        }
      }
    }
    return tags.filter((t) => t.operations.length > 0)
  },
}
