/**
 * Finds {{name}} parameters in SQL for live hints in the editor. It mirrors internal/params on the
 * server (which stays authoritative): text inside string literals, quoted identifiers and comments
 * is ignored.
 */
export type Dialect = 'postgres' | 'mysql' | 'mssql' | 'sqlite'

export const BUILTINS = ['now', 'today', 'yesterday', 'start_of_week', 'start_of_month', 'start_of_last_month', 'end_of_last_month', 'start_of_year'] as const

export type ParamType = 'text' | 'integer' | 'decimal' | 'boolean' | 'date' | 'datetime'
export const PARAM_TYPES: ParamType[] = ['text', 'integer', 'decimal', 'boolean', 'date', 'datetime']

export function isBuiltin(name: string): boolean {
  return (BUILTINS as readonly string[]).includes(name)
}

const PLACEHOLDER = /\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g

/** Code segments of sql, with literals and comments removed. */
export function codeSegments(dialect: Dialect, sql: string): string[] {
  const out: string[] = []
  let start = 0
  let i = 0
  const cut = (end: number) => {
    if (end > start) out.push(sql.slice(start, end))
  }
  const skipQuoted = (closer: string, backslash: boolean) => {
    let j = i + 1
    for (; j < sql.length; j++) {
      if (backslash && sql[j] === '\\') {
        j++
        continue
      }
      if (sql[j] === closer) {
        if (sql[j + 1] === closer) {
          j++
          continue
        }
        return j + 1
      }
    }
    return sql.length
  }
  while (i < sql.length) {
    const c = sql[i]!
    let end = -1
    if (c === '-' && sql[i + 1] === '-') end = lineEnd(sql, i)
    else if (c === '#' && dialect === 'mysql') end = lineEnd(sql, i)
    else if (c === '/' && sql[i + 1] === '*') {
      const close = sql.indexOf('*/', i + 2)
      end = close < 0 ? sql.length : close + 2
    } else if (c === "'") end = skipQuoted("'", dialect === 'mysql' || (dialect === 'postgres' && /[eE]/.test(sql[i - 1] ?? '')))
    else if (c === '"') end = skipQuoted('"', dialect === 'mysql')
    else if (c === '`' && (dialect === 'mysql' || dialect === 'sqlite')) end = skipQuoted('`', false)
    else if (c === '[' && (dialect === 'mssql' || dialect === 'sqlite')) end = skipQuoted(']', false)
    else if (c === '$' && dialect === 'postgres') {
      const tag = /^\$[A-Za-z_]*\$/.exec(sql.slice(i))?.[0]
      if (tag) {
        const close = sql.indexOf(tag, i + tag.length)
        end = close < 0 ? sql.length : close + tag.length
      }
    }
    if (end >= 0) {
      cut(i)
      i = end
      start = end
    } else {
      i++
    }
  }
  cut(sql.length)
  return out
}

function lineEnd(s: string, i: number) {
  const j = s.indexOf('\n', i)
  return j < 0 ? s.length : j
}

/** Distinct parameter names in order of first appearance. */
export function findParams(dialect: Dialect, sql: string): string[] {
  const out: string[] = []
  for (const seg of codeSegments(dialect, sql)) {
    for (const m of seg.matchAll(PLACEHOLDER)) {
      if (!out.includes(m[1]!)) out.push(m[1]!)
    }
  }
  return out
}
