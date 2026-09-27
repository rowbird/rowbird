import './assets/main.css'

import { createPinia } from 'pinia'
import { createApp } from 'vue'

import App from './App.vue'
import { createAppI18n } from './i18n'
import { createAppRouter, installGuards } from './router'
import { usePreferencesStore } from './stores/preferences'
import { useSessionStore } from './stores/session'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)

const prefs = usePreferencesStore(pinia)
app.use(createAppI18n(prefs.locale))

const router = createAppRouter()
installGuards(router, useSessionStore(pinia))
app.use(router)

app.mount('#app')
