<template>
  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('auth.welcomeBack') }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('auth.signInToAccount') }}
        </p>
      </div>

      <div v-if="!backendModeEnabled && (linuxdoOAuthEnabled || oidcOAuthEnabled)" class="space-y-4">
        <LinuxDoOAuthSection
          v-if="linuxdoOAuthEnabled"
          :disabled="isLoading"
          :show-divider="false"
        />
        <OidcOAuthSection
          v-if="oidcOAuthEnabled"
          :disabled="isLoading"
          :provider-name="oidcOAuthProviderName"
          :show-divider="false"
        />
        <div class="flex items-center gap-3">
          <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('auth.oauthOrContinue') }}
          </span>
          <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
        </div>
      </div>

      <div class="rounded-2xl border border-gray-200 bg-gray-50 p-1 dark:border-dark-700 dark:bg-dark-800/80">
        <div class="grid grid-cols-2 gap-1">
          <button
            type="button"
            :class="modeButtonClass('password')"
            :disabled="isLoading"
            @click="switchLoginMode('password')"
          >
            <Icon name="mail" size="sm" class="mr-2" />
            {{ t('auth.loginWithEmail') }}
          </button>
          <button
            type="button"
            :class="modeButtonClass('redeem')"
            :disabled="isLoading"
            @click="switchLoginMode('redeem')"
          >
            <Icon name="gift" size="sm" class="mr-2" />
            {{ t('auth.loginWithRedeemCode') }}
          </button>
        </div>
      </div>

      <form @submit.prevent="handleSubmit" class="space-y-5">
        <template v-if="loginMode === 'password'">
          <div>
            <label for="email" class="input-label">
              {{ t('auth.emailLabel') }}
            </label>
            <div class="relative">
              <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
                <Icon name="mail" size="md" class="text-gray-400 dark:text-dark-500" />
              </div>
              <input
                id="email"
                v-model="formData.email"
                type="email"
                required
                autofocus
                autocomplete="email"
                :disabled="isLoading"
                class="input pl-11"
                :class="{ 'input-error': errors.email }"
                :placeholder="t('auth.emailPlaceholder')"
              />
            </div>
            <p v-if="errors.email" class="input-error-text">
              {{ errors.email }}
            </p>
          </div>

          <div>
            <label for="password" class="input-label">
              {{ t('auth.passwordLabel') }}
            </label>
            <div class="relative">
              <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
                <Icon name="lock" size="md" class="text-gray-400 dark:text-dark-500" />
              </div>
              <input
                id="password"
                v-model="formData.password"
                :type="showPassword ? 'text' : 'password'"
                required
                autocomplete="current-password"
                :disabled="isLoading"
                class="input pl-11 pr-11"
                :class="{ 'input-error': errors.password }"
                :placeholder="t('auth.passwordPlaceholder')"
              />
              <button
                type="button"
                @click="showPassword = !showPassword"
                class="absolute inset-y-0 right-0 flex items-center pr-3.5 text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-dark-300"
              >
                <Icon v-if="showPassword" name="eyeOff" size="md" />
                <Icon v-else name="eye" size="md" />
              </button>
            </div>
            <div class="mt-1 flex items-center justify-between">
              <p v-if="errors.password" class="input-error-text">
                {{ errors.password }}
              </p>
              <span v-else></span>
              <router-link
                v-if="passwordResetEnabled && !backendModeEnabled"
                to="/forgot-password"
                class="text-sm font-medium text-primary-600 transition-colors hover:text-primary-500 dark:text-primary-400 dark:hover:text-primary-300"
              >
                {{ t('auth.forgotPassword') }}
              </router-link>
            </div>
          </div>
        </template>

        <template v-else>
          <div class="rounded-xl border border-primary-200 bg-primary-50 p-4 text-sm text-primary-700 dark:border-primary-800/50 dark:bg-primary-900/20 dark:text-primary-300">
            <p class="font-medium">{{ t('auth.redeemCodeHint') }}</p>
          </div>

          <div>
            <label for="invitation-code" class="input-label">
              {{ t('auth.redeemCodeLabel') }}
            </label>
            <div class="relative">
              <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
                <Icon name="gift" size="md" class="text-gray-400 dark:text-dark-500" />
              </div>
              <input
                id="invitation-code"
                v-model="inviteForm.invitation_code"
                type="text"
                required
                autofocus
                autocapitalize="characters"
                :disabled="isLoading"
                class="input pl-11 uppercase"
                :class="{ 'input-error': errors.invitationCode }"
                :placeholder="t('auth.redeemCodePlaceholder')"
              />
            </div>
            <p v-if="errors.invitationCode" class="input-error-text">
              {{ errors.invitationCode }}
            </p>
          </div>
        </template>

        <div v-if="turnstileEnabled && turnstileSiteKey">
          <TurnstileWidget
            ref="turnstileRef"
            :site-key="turnstileSiteKey"
            @verify="onTurnstileVerify"
            @expire="onTurnstileExpire"
            @error="onTurnstileError"
          />
          <p v-if="errors.turnstile" class="input-error-text mt-2 text-center">
            {{ errors.turnstile }}
          </p>
        </div>

        <transition name="fade">
          <div
            v-if="errorMessage"
            class="rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-800/50 dark:bg-red-900/20"
          >
            <div class="flex items-start gap-3">
              <div class="flex-shrink-0">
                <Icon name="exclamationCircle" size="md" class="text-red-500" />
              </div>
              <p class="text-sm text-red-700 dark:text-red-400">
                {{ errorMessage }}
              </p>
            </div>
          </div>
        </transition>

        <button
          type="submit"
          :disabled="isLoading || (turnstileEnabled && !turnstileToken)"
          class="btn btn-primary w-full"
        >
          <svg
            v-if="isLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
          </svg>
          <Icon v-else :name="loginMode === 'redeem' ? 'gift' : 'login'" size="md" class="mr-2" />
          {{ submitLabel }}
        </button>
      </form>
    </div>

    <template v-if="!backendModeEnabled" #footer>
      <p class="text-gray-500 dark:text-dark-400">
        {{ t('auth.dontHaveAccount') }}
        <router-link
          to="/register"
          class="font-medium text-primary-600 transition-colors hover:text-primary-500 dark:text-primary-400 dark:hover:text-primary-300"
        >
          {{ t('auth.signUp') }}
        </router-link>
      </p>
    </template>
  </AuthLayout>

  <TotpLoginModal
    v-if="show2FAModal"
    ref="totpModalRef"
    :temp-token="totpTempToken"
    :user-email-masked="totpUserEmailMasked"
    @verify="handle2FAVerify"
    @cancel="handle2FACancel"
  />
</template>

<script setup lang="ts">
import { computed, reactive, ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import TotpLoginModal from '@/components/auth/TotpLoginModal.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/TurnstileWidget.vue'
import { useAuthStore, useAppStore } from '@/stores'
import { getPublicSettings, isTotp2FARequired } from '@/api/auth'
import type { TotpLoginResponse } from '@/types'

const { t } = useI18n()

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const appStore = useAppStore()

const isLoading = ref(false)
const errorMessage = ref('')
const showPassword = ref(false)
const loginMode = ref<'password' | 'redeem'>('password')

const turnstileEnabled = ref(false)
const turnstileSiteKey = ref('')
const linuxdoOAuthEnabled = ref(false)
const backendModeEnabled = ref(false)
const oidcOAuthEnabled = ref(false)
const oidcOAuthProviderName = ref('OIDC')
const passwordResetEnabled = ref(false)

const turnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const turnstileToken = ref('')

const show2FAModal = ref(false)
const totpTempToken = ref('')
const totpUserEmailMasked = ref('')
const totpModalRef = ref<InstanceType<typeof TotpLoginModal> | null>(null)

const formData = reactive({
  email: '',
  password: ''
})

const inviteForm = reactive({
  invitation_code: ''
})

const errors = reactive({
  email: '',
  password: '',
  invitationCode: '',
  turnstile: ''
})

const submitLabel = computed(() => {
  if (loginMode.value === 'redeem') {
    return isLoading.value ? t('auth.redeemSigningIn') : t('auth.redeemSignIn')
  }
  return isLoading.value ? t('auth.signingIn') : t('auth.signIn')
})

onMounted(async () => {
  const expiredFlag = sessionStorage.getItem('auth_expired')
  if (expiredFlag) {
    sessionStorage.removeItem('auth_expired')
    const message = t('auth.reloginRequired')
    errorMessage.value = message
    appStore.showWarning(message)
  }

  try {
    const settings = await getPublicSettings()
    turnstileEnabled.value = settings.turnstile_enabled
    turnstileSiteKey.value = settings.turnstile_site_key || ''
    linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled
    backendModeEnabled.value = settings.backend_mode_enabled
    oidcOAuthEnabled.value = settings.oidc_oauth_enabled
    oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
    passwordResetEnabled.value = settings.password_reset_enabled
  } catch (error) {
    console.error('Failed to load public settings:', error)
  }
})

function modeButtonClass(mode: 'password' | 'redeem'): string {
  const isActive = loginMode.value === mode
  return [
    'inline-flex items-center justify-center rounded-xl px-4 py-2.5 text-sm font-medium transition-colors',
    isActive
      ? 'bg-white text-primary-600 shadow-sm dark:bg-dark-700 dark:text-primary-300'
      : 'text-gray-500 hover:text-gray-700 dark:text-dark-300 dark:hover:text-white'
  ].join(' ')
}

function switchLoginMode(mode: 'password' | 'redeem'): void {
  loginMode.value = mode
  errorMessage.value = ''
  errors.email = ''
  errors.password = ''
  errors.invitationCode = ''
  errors.turnstile = ''
}

function onTurnstileVerify(token: string): void {
  turnstileToken.value = token
  errors.turnstile = ''
}

function onTurnstileExpire(): void {
  turnstileToken.value = ''
  errors.turnstile = t('auth.turnstileExpired')
}

function onTurnstileError(): void {
  turnstileToken.value = ''
  errors.turnstile = t('auth.turnstileFailed')
}

function validateForm(): boolean {
  errors.email = ''
  errors.password = ''
  errors.invitationCode = ''
  errors.turnstile = ''

  let isValid = true

  if (loginMode.value === 'password') {
    if (!formData.email.trim()) {
      errors.email = t('auth.emailRequired')
      isValid = false
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(formData.email)) {
      errors.email = t('auth.invalidEmail')
      isValid = false
    }

    if (!formData.password) {
      errors.password = t('auth.passwordRequired')
      isValid = false
    } else if (formData.password.length < 6) {
      errors.password = t('auth.passwordMinLength')
      isValid = false
    }
  } else {
    inviteForm.invitation_code = inviteForm.invitation_code.trim().toUpperCase()
    if (!inviteForm.invitation_code) {
      errors.invitationCode = t('auth.redeemCodeRequired')
      isValid = false
    }
  }

  if (turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

async function redirectAfterPasswordLogin(): Promise<void> {
  const redirectTo = (route.query.redirect as string) || '/dashboard'
  await router.push(redirectTo)
}

async function redirectAfterRedeemLogin(): Promise<void> {
  await router.push({
    path: '/profile',
    query: { inviteBootstrap: '1' }
  })
}

function resetTurnstile(): void {
  if (turnstileRef.value) {
    turnstileRef.value.reset()
    turnstileToken.value = ''
  }
}

function setErrorMessage(error: unknown, fallback: string): void {
  const err = error as { message?: string; response?: { data?: { detail?: string; message?: string } } }
  errorMessage.value = err.response?.data?.detail || err.response?.data?.message || err.message || fallback
  appStore.showError(errorMessage.value)
}

async function handlePasswordLogin(): Promise<void> {
  const response = await authStore.login({
    email: formData.email,
    password: formData.password,
    turnstile_token: turnstileEnabled.value ? turnstileToken.value : undefined
  })

  if (isTotp2FARequired(response)) {
    const totpResponse = response as TotpLoginResponse
    totpTempToken.value = totpResponse.temp_token || ''
    totpUserEmailMasked.value = totpResponse.user_email_masked || ''
    show2FAModal.value = true
    isLoading.value = false
    return
  }

  appStore.showSuccess(t('auth.loginSuccess'))
  await redirectAfterPasswordLogin()
}

async function handleRedeemLogin(): Promise<void> {
  await authStore.inviteLogin({
    invitation_code: inviteForm.invitation_code,
    turnstile_token: turnstileEnabled.value ? turnstileToken.value : undefined
  })

  appStore.showSuccess(t('auth.redeemLoginSuccess'))
  await redirectAfterRedeemLogin()
}

async function handleSubmit(): Promise<void> {
  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  isLoading.value = true

  try {
    if (loginMode.value === 'redeem') {
      await handleRedeemLogin()
      return
    }

    await handlePasswordLogin()
  } catch (error: unknown) {
    resetTurnstile()
    setErrorMessage(error, loginMode.value === 'redeem' ? t('auth.redeemLoginFailed') : t('auth.loginFailed'))
  } finally {
    isLoading.value = false
  }
}

async function handle2FAVerify(code: string): Promise<void> {
  if (totpModalRef.value) {
    totpModalRef.value.setVerifying(true)
  }

  try {
    await authStore.login2FA(totpTempToken.value, code)
    show2FAModal.value = false
    appStore.showSuccess(t('auth.loginSuccess'))
    await redirectAfterPasswordLogin()
  } catch (error: unknown) {
    const err = error as { message?: string; response?: { data?: { message?: string } } }
    const message = err.response?.data?.message || err.message || t('profile.totp.loginFailed')

    if (totpModalRef.value) {
      totpModalRef.value.setError(message)
      totpModalRef.value.setVerifying(false)
    }
  }
}

function handle2FACancel(): void {
  show2FAModal.value = false
  totpTempToken.value = ''
  totpUserEmailMasked.value = ''
}
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: all 0.3s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
