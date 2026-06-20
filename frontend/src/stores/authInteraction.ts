import { defineStore } from 'pinia'

export const useAuthInteractionStore = defineStore('authInteraction', {
  state: () => ({
    isTyping: false,
    showPassword: false,
    passwordLength: 0,
  }),
})
