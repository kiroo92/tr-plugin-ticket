import { definePlugin } from '@tokenflux/tokenrouter-plugin-sdk'
import UserPage from './UserPage.vue'
import AdminPage from './AdminPage.vue'
import './style.css'

if (import.meta.env.DEV) await import('./dev')

definePlugin({ pages: { user: UserPage, admin: AdminPage } })
