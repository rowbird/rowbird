import { createHash, createSign, generateKeyPairSync, randomBytes } from 'node:crypto'
import type { IncomingMessage, ServerResponse } from 'node:http'

/**
 * A minimal OpenID Connect provider under /oidc of the fake server: discovery, keys, an
 * authorization endpoint that signs in the configured person at once, and a token endpoint that
 * checks the client and the PKCE verifier. Enough for the single sign-on journey.
 */
export const OIDC_CLIENT = { id: 'rowbird-e2e', secret: 'e2e-client-secret' }
export const OIDC_PERSON = { sub: 'e2e-ana', email: 'ana@example.com', name: 'Ana Admin' }

const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'e2e', alg: 'RS256', use: 'sig' }
const grants = new Map<string, { nonce: string; challenge: string; redirect: string }>()

const b64url = (b: Buffer | string) => Buffer.from(b).toString('base64url')

function idToken(issuer: string, nonce: string) {
  const now = Math.floor(Date.now() / 1000)
  const header = b64url(JSON.stringify({ alg: 'RS256', typ: 'JWT', kid: 'e2e' }))
  const claims = b64url(JSON.stringify({
    iss: issuer, aud: OIDC_CLIENT.id, sub: OIDC_PERSON.sub, iat: now, exp: now + 300, nonce,
    email: OIDC_PERSON.email, email_verified: true, name: OIDC_PERSON.name,
  }))
  const signature = createSign('RSA-SHA256').update(`${header}.${claims}`).sign(privateKey)
  return `${header}.${claims}.${b64url(signature)}`
}

/** Answers a request under /oidc; the caller already read the body. */
export function fakeOIDC(req: IncomingMessage, res: ServerResponse, url: URL, body: Buffer) {
  const issuer = `http://${req.headers.host}/oidc`
  const json = (status: number, value: unknown) => {
    res.writeHead(status, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(value))
  }
  switch (url.pathname) {
    case '/oidc/.well-known/openid-configuration':
      return json(200, {
        issuer, authorization_endpoint: `${issuer}/authorize`, token_endpoint: `${issuer}/token`, jwks_uri: `${issuer}/jwks`,
        response_types_supported: ['code'], subject_types_supported: ['public'], id_token_signing_alg_values_supported: ['RS256'],
        code_challenge_methods_supported: ['S256'],
      })
    case '/oidc/jwks':
      return json(200, { keys: [jwk] })
    case '/oidc/authorize': {
      const q = url.searchParams
      if (q.get('client_id') !== OIDC_CLIENT.id || q.get('code_challenge_method') !== 'S256') return json(400, { error: 'invalid_request' })
      const code = randomBytes(16).toString('hex')
      grants.set(code, { nonce: q.get('nonce') ?? '', challenge: q.get('code_challenge') ?? '', redirect: q.get('redirect_uri') ?? '' })
      const back = new URL(q.get('redirect_uri') ?? '')
      back.searchParams.set('code', code)
      back.searchParams.set('state', q.get('state') ?? '')
      res.writeHead(302, { Location: back.toString() })
      return res.end()
    }
    case '/oidc/token': {
      const form = new URLSearchParams(body.toString('utf8'))
      const basic = Buffer.from((req.headers.authorization ?? '').replace(/^Basic /, ''), 'base64').toString('utf8')
      const [id, secret] = basic ? basic.split(':').map(decodeURIComponent) : [form.get('client_id'), form.get('client_secret')]
      const grant = grants.get(form.get('code') ?? '')
      grants.delete(form.get('code') ?? '')
      const verifier = createHash('sha256').update(form.get('code_verifier') ?? '').digest('base64url')
      if (id !== OIDC_CLIENT.id || secret !== OIDC_CLIENT.secret) return json(401, { error: 'invalid_client' })
      if (!grant || grant.challenge !== verifier || grant.redirect !== form.get('redirect_uri')) return json(400, { error: 'invalid_grant' })
      return json(200, { access_token: randomBytes(16).toString('hex'), token_type: 'Bearer', expires_in: 300, id_token: idToken(issuer, grant.nonce) })
    }
  }
  return json(404, { error: 'not_found' })
}
