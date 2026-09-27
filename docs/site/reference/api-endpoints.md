---
outline: false
---

# API endpoints

Every operation of the HTTP API, grouped by area, as declared in
[`api/openapi.yaml`](https://github.com/rowbird/rowbird/blob/main/api/openapi.yaml). "Access" is
the minimum role of a signed-in user and the minimum scope of an API key; "session only"
operations do not accept API keys. Load the OpenAPI file into any OpenAPI viewer or client
generator for request and response schemas.

<ApiEndpoints />
