import { ref } from 'vue'

import { api, type Schemas } from '@/api/client'

/**
 * The newer release the update check found, shared by the sidebar and Settings > About. The check
 * runs on the server once a day, so the sidebar asks again every hour: a tab left open all day still
 * learns about a release.
 */
const newVersion = ref('')

export const NEW_VERSION_POLL_MS = 60 * 60 * 1000

/** Records what an About answer says about updates. */
export function noteAbout(about: Schemas['About'] | null | undefined) {
  newVersion.value = about?.update_available && about.latest_version ? about.latest_version : ''
}

/** Asks the server again. Failures keep the last answer: the notice is a hint, not an error. */
export async function refreshNewVersion() {
  const { data } = await api.GET('/api/v1/system/about')
  if (data) noteAbout(data)
}

export function useNewVersion() {
  return newVersion
}
