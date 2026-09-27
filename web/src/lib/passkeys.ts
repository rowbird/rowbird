import {
  browserSupportsWebAuthn,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
  startAuthentication,
  startRegistration,
} from '@simplewebauthn/browser'

/**
 * Passkeys through the browser's WebAuthn API. The server sends the options with binary values in
 * base64url and takes the answers back as JSON (docs/spec/05-api.md).
 */
export function passkeysSupported(): boolean {
  return browserSupportsWebAuthn()
}

export async function createPasskey(options: Record<string, unknown>): Promise<Record<string, unknown>> {
  const res = await startRegistration({ optionsJSON: options as unknown as PublicKeyCredentialCreationOptionsJSON })
  return res as unknown as Record<string, unknown>
}

export async function getPasskey(options: Record<string, unknown>): Promise<Record<string, unknown>> {
  const res = await startAuthentication({ optionsJSON: options as unknown as PublicKeyCredentialRequestOptionsJSON })
  return res as unknown as Record<string, unknown>
}

/** The user closed the browser's passkey prompt, or it timed out: not an error worth showing. */
export function passkeyCancelled(e: unknown): boolean {
  const name = (e as { name?: string } | null)?.name
  const code = (e as { code?: string } | null)?.code
  return name === 'NotAllowedError' || name === 'AbortError' || code === 'ERROR_CEREMONY_ABORTED'
}
