<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-medium text-gray-900 dark:text-white">
        {{ t('profile.editProfile') }}
      </h2>
    </div>
    <div class="px-6 py-6">
      <form @submit.prevent="handleUpdateProfile" class="space-y-4">
        <div>
          <label for="username" class="input-label">
            {{ t('profile.username') }}
          </label>
          <input
            id="username"
            v-model="username"
            type="text"
            class="input"
            :placeholder="t('profile.enterUsername')"
          />
        </div>

        <div>
          <label for="email" class="input-label">
            {{ t('profile.email') }}
          </label>
          <template v-if="canEditEmail">
            <input
              id="email"
              v-model="email"
              type="email"
              class="input"
              :placeholder="t('profile.enterEmail')"
            />
            <p class="mt-1 text-xs text-amber-600 dark:text-amber-300">
              {{ t('profile.emailBootstrapHint') }}
            </p>
          </template>
          <template v-else>
            <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-700 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">
              <p class="break-all">{{ email }}</p>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('profile.emailChangeAdminNote') }}
              </p>
            </div>
          </template>
        </div>

        <div class="flex justify-end pt-4">
          <button type="submit" :disabled="loading" class="btn btn-primary">
            {{ loading ? t('profile.updating') : t('profile.updateProfile') }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { userAPI } from '@/api'

const props = defineProps<{
  initialUsername: string
  initialEmail: string
  canEditEmail: boolean
}>()

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const username = ref(props.initialUsername)
const email = ref(props.initialEmail)
const loading = ref(false)

watch(() => props.initialUsername, (val) => {
  username.value = val
})

watch(() => props.initialEmail, (val) => {
  email.value = val
})

const handleUpdateProfile = async () => {
  if (!username.value.trim()) {
    appStore.showError(t('profile.usernameRequired'))
    return
  }

  const trimmedEmail = email.value.trim()
  if (props.canEditEmail && !trimmedEmail) {
    appStore.showError(t('profile.emailRequired'))
    return
  }

  loading.value = true
  try {
    const updatedUser = await userAPI.updateProfile({
      username: username.value,
      ...(props.canEditEmail ? { email: trimmedEmail } : {})
    })
    authStore.user = updatedUser
    appStore.showSuccess(t('profile.updateSuccess'))
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('profile.updateFailed'))
  } finally {
    loading.value = false
  }
}
</script>
