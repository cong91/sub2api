<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <div class="grid grid-cols-1 gap-6 sm:grid-cols-3">
        <StatCard
          :title="t('profile.accountBalance')"
          :value="formatCurrency(user?.balance || 0)"
          :icon="WalletIcon"
          icon-variant="success"
        />
        <StatCard
          :title="t('profile.concurrencyLimit')"
          :value="user?.concurrency || 0"
          :icon="BoltIcon"
          icon-variant="warning"
        />
        <StatCard
          :title="t('profile.memberSince')"
          :value="formatDate(user?.created_at || '', { year: 'numeric', month: 'long' })"
          :icon="CalendarIcon"
          icon-variant="primary"
        />
      </div>

      <div
        v-if="showInviteBootstrapGuide"
        class="card border-amber-200 bg-amber-50 p-6 dark:border-amber-800/50 dark:bg-amber-900/20"
      >
        <div class="flex items-start gap-4">
          <div class="rounded-xl bg-amber-100 p-3 text-amber-600 dark:bg-amber-900/50 dark:text-amber-300">
            <Icon name="gift" size="lg" />
          </div>
          <div class="space-y-3">
            <div>
              <h3 class="text-lg font-semibold text-amber-900 dark:text-amber-100">
                {{ t('profile.inviteBootstrap.title') }}
              </h3>
              <p class="mt-1 text-sm text-amber-800 dark:text-amber-200">
                {{ t('profile.inviteBootstrap.description') }}
              </p>
            </div>

            <div
              v-if="isInviteBootstrapAccount && user?.email"
              class="rounded-xl border border-amber-200 bg-white/70 px-4 py-3 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/30 dark:text-amber-100"
            >
              <span class="font-medium">{{ t('profile.inviteBootstrap.temporaryEmailLabel') }}:</span>
              <span class="break-all"> {{ user.email }}</span>
            </div>

            <ol class="list-decimal space-y-2 pl-5 text-sm text-amber-900 dark:text-amber-100">
              <li>{{ t('profile.inviteBootstrap.steps.username') }}</li>
              <li>{{ t('profile.inviteBootstrap.steps.password') }}</li>
              <li>{{ t('profile.inviteBootstrap.steps.email') }}</li>
              <li>{{ t('profile.inviteBootstrap.steps.adminAfterSwitch') }}</li>
            </ol>

            <div class="rounded-xl border border-amber-200 bg-amber-100/70 px-4 py-3 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-900/40 dark:text-amber-100">
              {{ t('profile.inviteBootstrap.notice') }}
            </div>
          </div>
        </div>
      </div>

      <ProfileInfoCard :user="user" />

      <div
        v-if="contactInfo"
        class="card border-primary-200 bg-primary-50 p-6 dark:border-primary-800/50 dark:bg-primary-900/20"
      >
        <div class="flex items-center gap-4">
          <div class="rounded-xl bg-primary-100 p-3 text-primary-600 dark:bg-primary-900/50 dark:text-primary-300">
            <Icon name="chat" size="lg" />
          </div>
          <div>
            <h3 class="font-semibold text-primary-800 dark:text-primary-200">
              {{ t('common.contactSupport') }}
            </h3>
            <p class="text-sm font-medium">{{ contactInfo }}</p>
          </div>
        </div>
      </div>

      <ProfileEditForm
        :initial-username="user?.username || ''"
        :initial-email="user?.email || ''"
        :can-edit-email="isInviteBootstrapAccount"
      />

      <ProfileBalanceNotifyCard
        v-if="user && balanceLowNotifyEnabled"
        :enabled="user.balance_notify_enabled ?? true"
        :threshold="user.balance_notify_threshold"
        :extra-emails="user.balance_notify_extra_emails ?? []"
        :system-default-threshold="systemDefaultThreshold"
        :user-email="user.email"
      />

      <ProfilePasswordForm />
      <ProfileTotpCard />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { authAPI } from '@/api'
import { Icon } from '@/components/icons'
import StatCard from '@/components/common/StatCard.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import ProfileBalanceNotifyCard from '@/components/user/profile/ProfileBalanceNotifyCard.vue'
import ProfileEditForm from '@/components/user/profile/ProfileEditForm.vue'
import ProfileInfoCard from '@/components/user/profile/ProfileInfoCard.vue'
import ProfilePasswordForm from '@/components/user/profile/ProfilePasswordForm.vue'
import ProfileTotpCard from '@/components/user/profile/ProfileTotpCard.vue'
import { useAuthStore } from '@/stores/auth'
import { formatDate } from '@/utils/format'

const { t } = useI18n()
const authStore = useAuthStore()

const user = computed(() => authStore.user)
const contactInfo = ref('')
const balanceLowNotifyEnabled = ref(false)
const systemDefaultThreshold = ref(0)

const isInviteBootstrapAccount = computed(() =>
  Boolean(user.value?.email?.endsWith('@invite-login.invalid'))
)
const showInviteBootstrapGuide = computed(() => isInviteBootstrapAccount.value)

const WalletIcon = {
  render: () =>
    h('svg', { fill: 'none', viewBox: '0 0 24 24', stroke: 'currentColor', 'stroke-width': '1.5' }, [
      h('path', { d: 'M21 12a2.25 2.25 0 00-2.25-2.25H15a3 3 0 11-6 0H5.25A2.25 2.25 0 003 12' })
    ])
}

const BoltIcon = {
  render: () =>
    h('svg', { fill: 'none', viewBox: '0 0 24 24', stroke: 'currentColor', 'stroke-width': '1.5' }, [
      h('path', { d: 'm3.75 13.5 10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z' })
    ])
}

const CalendarIcon = {
  render: () =>
    h('svg', { fill: 'none', viewBox: '0 0 24 24', stroke: 'currentColor', 'stroke-width': '1.5' }, [
      h('path', { d: 'M6.75 3v2.25M17.25 3v2.25' })
    ])
}

onMounted(async () => {
  try {
    const settings = await authAPI.getPublicSettings()
    contactInfo.value = settings.contact_info || ''
    balanceLowNotifyEnabled.value = settings.balance_low_notify_enabled ?? false
    systemDefaultThreshold.value = settings.balance_low_notify_threshold ?? 0
  } catch (error) {
    console.error('Failed to load settings:', error)
  }
})

function formatCurrency(value: number): string {
  return `$${value.toFixed(2)}`
}
</script>
