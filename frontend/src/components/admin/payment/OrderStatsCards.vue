<template>
  <div class="space-y-4">
    <!-- Revenue by Currency -->
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      <div v-for="rev in stats.revenue_by_currency" :key="rev.currency" class="card p-4">
        <div class="flex items-center gap-3">
          <div class="rounded-lg bg-green-100 p-2 dark:bg-green-900/30">
            <Icon name="dollar" size="md" class="text-green-600 dark:text-green-400" :stroke-width="2" />
          </div>
          <div class="min-w-0">
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('payment.admin.revenue') }} ({{ rev.currency }})
            </p>
            <p class="text-xl font-bold text-gray-900 dark:text-white">
              {{ formatCurrency(rev.total_amount, rev.currency) }}
            </p>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('payment.admin.today') }}: {{ formatCurrency(rev.today_amount, rev.currency) }}
              · {{ rev.today_count }}/{{ rev.total_count }} {{ t('payment.admin.orders') }}
            </p>
          </div>
        </div>
      </div>
    </div>

    <!-- Summary row -->
    <div class="grid grid-cols-2 gap-4 lg:grid-cols-3">
      <!-- Today Orders -->
      <div class="card p-4">
        <div class="flex items-center gap-3">
          <div class="rounded-lg bg-purple-100 p-2 dark:bg-purple-900/30">
            <Icon name="chart" size="md" class="text-purple-600 dark:text-purple-400" :stroke-width="2" />
          </div>
          <div>
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.todayOrders') }}</p>
            <p class="text-xl font-bold text-gray-900 dark:text-white">{{ stats.today_count }}</p>
          </div>
        </div>
      </div>

      <!-- Total Orders -->
      <div class="card p-4">
        <div class="flex items-center gap-3">
          <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30">
            <Icon name="creditCard" size="md" class="text-blue-600 dark:text-blue-400" :stroke-width="2" />
          </div>
          <div>
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.totalOrders') }}</p>
            <p class="text-xl font-bold text-gray-900 dark:text-white">{{ stats.total_count }}</p>
          </div>
        </div>
      </div>

      <!-- Pending Orders -->
      <div class="card p-4">
        <div class="flex items-center gap-3">
          <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30">
            <Icon name="chart" size="md" class="text-amber-600 dark:text-amber-400" :stroke-width="2" />
          </div>
          <div>
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.pendingOrders') }}</p>
            <p class="text-xl font-bold text-gray-900 dark:text-white">{{ stats.pending_orders }}</p>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { DashboardStats } from '@/types/payment'

const { t } = useI18n()

defineProps<{
  stats: DashboardStats
}>()

const CURRENCY_SYMBOLS: Record<string, string> = {
  USD: '$',
  VND: '₫',
  CNY: '¥',
  EUR: '€',
  GBP: '£',
}

function formatCurrency(value: number, currency: string): string {
  const symbol = CURRENCY_SYMBOLS[currency] || currency + ' '
  // VND typically has no decimals
  if (currency === 'VND') {
    return symbol + Math.round(value).toLocaleString()
  }
  return symbol + value.toFixed(2)
}
</script>
