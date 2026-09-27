import { describe, expect, it } from 'vitest'

import { type Dialect, findParams } from '../params'

// The same cases as internal/params/params_test.go, so the live hints agree with the server.
describe('findParams', () => {
  it.each([
    ['postgres', 'select * from t where d >= {{today}} and x = {{ region }}', ['today', 'region']],
    ['postgres', "select '{{not}}', \"{{nor}}\" -- {{this}}\n/* {{or_this}} */ , {{yes}}", ['yes']],
    ['postgres', 'select $$ {{inside}} $$, {{a}}, {{a}}', ['a']],
    ['mssql', 'select [{{col}}], {{p}} from t', ['p']],
    ['mysql', 'select `{{col}}`, "{{str}}", {{p}} # {{c}}\n', ['p']],
    ['postgres', 'select {{1bad}}, {{ok_1}}, {{ spaced }}', ['ok_1', 'spaced']],
    ['postgres', "select E'it\\'s {{x}}', {{y}}", ['y']],
    ['sqlite', 'select [{{a}}], `{{b}}`, {{c}}', ['c']],
  ] as [Dialect, string, string[]][])('%s %s', (dialect, sql, want) => {
    expect(findParams(dialect, sql)).toEqual(want)
  })
})
