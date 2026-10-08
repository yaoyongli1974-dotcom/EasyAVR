import { defineStore } from 'pinia'
import { authApi } from '../api'
import type { User } from '../types'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem('easyavr_token') || '',
    user: null as User | null,
  }),
  getters: {
    isAuthenticated: (s) => !!s.token,
  },
  actions: {
    async login(username: string, password: string) {
      const res = await authApi.login(username, password)
      this.token = res.token
      this.user = res.user
      localStorage.setItem('easyavr_token', res.token)
    },
    async loadProfile() {
      if (!this.token) return
      this.user = await authApi.profile()
    },
    logout() {
      this.token = ''
      this.user = null
      localStorage.removeItem('easyavr_token')
    },
  },
})
