# ADR-0025: AI proposals with structured output, a schema budget and the assistant off by default

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

The AI assistant turns a description into SQL or a schedule (docs/spec/03-flows.md, section 3). Five
providers must be supported (OpenAI, Anthropic, Gemini, Ollama and OpenAI-compatible services),
their answers must be usable without parsing prose, databases can have thousands of tables, and
the hard rules forbid sending row data, contacting services the user did not configure, and running
anything the model writes.

## Decision

- **Providers carry, the service decides.** The `AIProvider` contract takes instructions, a
  request and the JSON Schema of the answer, and returns JSON. The AI service
  (`internal/ai`) builds the prompt, the schema and the validation, so the five providers only
  translate to their API: OpenAI uses a strict JSON schema, Anthropic structured outputs through
  the official Go SDK (with the host's environment ignored, so only the configured key is used),
  Gemini a response JSON schema, Ollama the chat format, and OpenAI-compatible services JSON mode
  by default with the schema in the instructions. Every provider passes `internal/plugin/aitest`.
- **Answers without optional fields.** Every field of an answer is required and "none" is an empty
  string, because not every API accepts optional fields or nulls in a strict schema.
- **A schema budget.** The schema goes compactly (one line per table) without the tables excluded
  from AI, never with rows or samples. Tables the request or the current SQL mention go first, and
  the rest fill about 60,000 characters; the proposal warns when some were left out.
- **Validation turns doubts into warnings.** An answer that is not the expected object, or a query
  without SQL, is an error (`ai.invalid_output`). SQL that may write or holds several statements,
  or a schedule that does not parse, is a warning; queries still run read-only when someone
  applies and runs them. Named parameters become suggested definitions.
- **Off by default.** Nothing is sent anywhere until an admin chooses a provider. Its key is kept
  like a channel's secrets (ADR-0022), with the workspace as the entity.
- **Per-user limit.** 20 requests per 10 minutes per user, in memory on each instance.

## Consequences

- Adding a provider means one package of API translation and its conformance test.
- Very large schemas can leave relevant tables out when the request does not name them; the
  warning tells the user to name them.
- The per-user limit is per instance; with several instances a user gets that many on each.
- JSON mode on OpenAI-compatible services depends on the model following the schema in the
  instructions; answers that do not are rejected rather than guessed at.
