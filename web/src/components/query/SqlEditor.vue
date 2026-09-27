<script setup lang="ts">
import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { MSSQL, MySQL, PostgreSQL, sql, SQLite } from '@codemirror/lang-sql'
import { bracketMatching, HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { Compartment, EditorState } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView, keymap, lineNumbers, MatchDecorator, placeholder, ViewPlugin, type ViewUpdate } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

import type { Dialect } from '@/lib/params'

/** SQL editor (CodeMirror 6) with the connection's dialect and schema completion. */
const props = defineProps<{
  dialect?: Dialect
  /** Table name -> column names, for autocompletion. */
  schema?: Record<string, string[]>
  readonly?: boolean
  placeholderText?: string
  label: string
}>()
const model = defineModel<string>({ required: true })
const emit = defineEmits<{ run: []; assistant: [] }>()

const host = ref<HTMLDivElement>()
let view: EditorView | null = null
const language = new Compartment()
const editable = new Compartment()

const dialects = { postgres: PostgreSQL, mysql: MySQL, mssql: MSSQL, sqlite: SQLite }

function languageExtension() {
  return sql({ dialect: dialects[props.dialect ?? 'postgres'], schema: props.schema ?? {}, upperCaseKeywords: true })
}

const highlight = HighlightStyle.define([
  { tag: tags.keyword, color: 'var(--sql-keyword)', fontWeight: '600' },
  { tag: [tags.string, tags.special(tags.string)], color: 'var(--sql-string)' },
  { tag: tags.number, color: 'var(--sql-number)' },
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: 'var(--sql-comment)', fontStyle: 'italic' },
])

// {{param}} placeholders are highlighted so they stand out from SQL.
const paramMark = new MatchDecorator({ regexp: /\{\{\s*[A-Za-z_][A-Za-z0-9_]*\s*\}\}/g, decoration: Decoration.mark({ class: 'cm-param' }) })
const paramHighlighter = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet
    constructor(v: EditorView) {
      this.decorations = paramMark.createDeco(v)
    }
    update(u: ViewUpdate) {
      this.decorations = paramMark.updateDeco(u, this.decorations)
    }
  },
  { decorations: (v) => v.decorations },
)

const theme = EditorView.theme({
  '&': { backgroundColor: 'var(--background)', color: 'var(--foreground)', fontSize: '13px', height: '100%' },
  '.cm-content': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', caretColor: 'var(--foreground)' },
  '.cm-gutters': { backgroundColor: 'var(--muted)', color: 'var(--muted-foreground)', borderRight: '1px solid var(--border)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in oklch, var(--muted) 50%, transparent)' },
  '&.cm-focused': { outline: 'none' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': { backgroundColor: 'color-mix(in oklch, var(--ring) 35%, transparent)' },
  '.cm-tooltip': { backgroundColor: 'var(--popover)', color: 'var(--popover-foreground)', border: '1px solid var(--border)' },
  '.cm-param': { color: 'var(--sql-param)', fontWeight: '600' },
  // The default placeholder is too faint to read (WCAG contrast).
  '.cm-placeholder': { color: 'var(--muted-foreground)' },
  '.cm-scroller': { overflow: 'auto' },
})

onMounted(() => {
  view = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: model.value,
      extensions: [
        lineNumbers(),
        history(),
        bracketMatching(),
        closeBrackets(),
        autocompletion(),
        syntaxHighlighting(highlight),
        paramHighlighter,
        theme,
        placeholder(props.placeholderText ?? ''),
        EditorView.lineWrapping,
        EditorView.contentAttributes.of({ 'aria-label': props.label }),
        language.of(languageExtension()),
        editable.of([EditorState.readOnly.of(!!props.readonly), EditorView.editable.of(!props.readonly)]),
        keymap.of([
          { key: 'Mod-Enter', run: () => (emit('run'), true) },
          // Opens the AI panel; replaces CodeMirror's select-parent binding.
          { key: 'Mod-i', run: () => (emit('assistant'), true) },
          indentWithTab,
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...historyKeymap,
          ...completionKeymap,
        ]),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) model.value = u.state.doc.toString()
        }),
      ],
    }),
  })
})

watch(model, (value) => {
  if (view && value !== view.state.doc.toString()) {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } })
  }
})
watch(
  () => [props.dialect, props.schema],
  () => view?.dispatch({ effects: language.reconfigure(languageExtension()) }),
)
watch(
  () => props.readonly,
  (ro) => view?.dispatch({ effects: editable.reconfigure([EditorState.readOnly.of(!!ro), EditorView.editable.of(!ro)]) }),
)

onBeforeUnmount(() => view?.destroy())

/** Inserts text at the cursor (used by the schema panel). */
function insert(text: string) {
  if (!view || props.readonly) return
  const { from, to } = view.state.selection.main
  view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length } })
  view.focus()
}

defineExpose({ insert })
</script>

<template>
  <div ref="host" class="h-full min-h-48 overflow-hidden rounded-md border" data-testid="sql-editor" />
</template>
