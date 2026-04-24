<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.dataImportTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="import-data-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.dataImportHint') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ t('admin.accounts.dataImportWarning') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex items-center justify-between gap-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200">
              {{ selectedFilesLabel || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">JSON (.json)</div>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" @click="openFilePicker">
            {{ t('common.chooseFile') }}
          </button>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          multiple
          @change="handleFileChange"
        />
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} — {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="import-data-form"
          :disabled="importing"
        >
          {{ importing ? t('admin.accounts.dataImporting') : t('admin.accounts.dataImportButton') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminDataImportResult, AdminImportData, AdminDataPayload } from '@/types'

const STRUCTURED_EXPORT_ONLY_ERROR = 'Structured export file must be imported alone'
const IMPORT_FORMAT_TYPE = 'sub2api-account-data'

interface ImportAggregateResult extends AdminDataImportResult {
  errors: NonNullable<AdminDataImportResult['errors']>
}

interface ParsedImportFile {
  data: AdminImportData
}

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const isPlainObject = (value: unknown): value is Record<string, unknown> => {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

const isStructuredDataPayload = (value: unknown): value is AdminDataPayload => {
  return isPlainObject(value) && Array.isArray(value.proxies) && Array.isArray(value.accounts)
}

const isCPAAccountObject = (value: unknown): value is Record<string, unknown> => {
  return isPlainObject(value) && (
    typeof value.refresh_token === 'string'
    || typeof value.access_token === 'string'
    || typeof value.id_token === 'string'
    || typeof value.account_id === 'string'
  )
}

const mergeImportResults = (results: AdminDataImportResult[]): ImportAggregateResult => {
  return results.reduce<ImportAggregateResult>((acc, item) => {
    acc.proxy_created += item.proxy_created
    acc.proxy_reused += item.proxy_reused
    acc.proxy_failed += item.proxy_failed
    acc.account_created += item.account_created
    acc.account_failed += item.account_failed
    if (item.errors?.length) {
      acc.errors.push(...item.errors)
    }
    return acc
  }, {
    proxy_created: 0,
    proxy_reused: 0,
    proxy_failed: 0,
    account_created: 0,
    account_failed: 0,
    errors: []
  })
}

const parseImportPayload = (raw: unknown): AdminImportData => {
  if (Array.isArray(raw)) {
    if (raw.every(isCPAAccountObject)) {
      return raw
    }
    throw new SyntaxError('Unsupported import array format')
  }

  if (isStructuredDataPayload(raw)) {
    return raw as AdminImportData
  }

  if (isCPAAccountObject(raw)) {
    return raw
  }

  throw new SyntaxError('Unsupported import object format')
}

const parseImportFiles = (entries: ParsedImportFile[]): AdminImportData[] => {
  const structuredPayloads: AdminImportData[] = []
  const cpaAccounts: Record<string, unknown>[] = []

  for (const entry of entries) {
    if (Array.isArray(entry.data)) {
      cpaAccounts.push(...entry.data)
      continue
    }

    if (isStructuredDataPayload(entry.data)) {
      structuredPayloads.push(entry.data)
      continue
    }

    cpaAccounts.push(entry.data)
  }

  if (structuredPayloads.length > 0 && cpaAccounts.length > 0) {
    throw new Error(STRUCTURED_EXPORT_ONLY_ERROR)
  }

  if (structuredPayloads.length > 1) {
    throw new Error(STRUCTURED_EXPORT_ONLY_ERROR)
  }

  if (structuredPayloads.length === 1) {
    return structuredPayloads
  }

  if (cpaAccounts.length > 0) {
    return [cpaAccounts]
  }

  return []
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const files = ref<File[]>([])
const result = ref<AdminDataImportResult | null>(null)

const fileInput = ref<HTMLInputElement | null>(null)
const selectedFilesLabel = computed(() => buildSelectedFilesLabel(files.value))
const errorItems = computed(() => result.value?.errors || [])

watch(
  () => props.show,
  (open) => {
    if (open) {
      files.value = []
      result.value = null
      if (fileInput.value) {
        fileInput.value.value = ''
      }
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  files.value = Array.from(target.files || [])
}

const handleClose = () => {
  if (importing.value) return
  emit('close')
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') {
    return sourceFile.text()
  }

  if (typeof sourceFile.arrayBuffer === 'function') {
    const buffer = await sourceFile.arrayBuffer()
    return new TextDecoder().decode(buffer)
  }

  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(sourceFile)
  })
}

const parseImportFilesFromText = async (selectedFiles: File[]): Promise<AdminImportData[]> => {
  const parsedEntries = await Promise.all(selectedFiles.map(async (sourceFile) => {
    const text = await readFileAsText(sourceFile)
    const raw = JSON.parse(text) as unknown
    return {
      data: parseImportPayload(raw)
    } satisfies ParsedImportFile
  }))

  return parseImportFiles(parsedEntries)
}

const buildSelectedFilesLabel = (selectedFiles: File[]): string => {
  if (selectedFiles.length === 0) {
    return ''
  }
  if (selectedFiles.length === 1) {
    return selectedFiles[0].name
  }
  return `${selectedFiles.length} JSON files selected`
}

const importDataPayloads = async (payloads: AdminImportData[]): Promise<ImportAggregateResult> => {
  const responses = await Promise.all(payloads.map((data) => adminAPI.accounts.importData({
    data,
    skip_default_group_bind: true
  })))

  return mergeImportResults(responses)
}

const handleImport = async () => {
  if (files.value.length === 0) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }

  importing.value = true
  try {
    const payloads = await parseImportFilesFromText(files.value)
    if (payloads.length === 0) {
      throw new SyntaxError('Unsupported import object format')
    }

    const structuredPayload = payloads.find((payload) => !Array.isArray(payload) && payload.type === IMPORT_FORMAT_TYPE)
    if (structuredPayload && payloads.length > 1) {
      throw new Error(STRUCTURED_EXPORT_ONLY_ERROR)
    }

    const res = await importDataPayloads(payloads)
    result.value = res

    const msgParams: Record<string, unknown> = {
      account_created: res.account_created,
      account_failed: res.account_failed,
      proxy_created: res.proxy_created,
      proxy_reused: res.proxy_reused,
      proxy_failed: res.proxy_failed,
    }
    if (res.account_failed > 0 || res.proxy_failed > 0) {
      appStore.showError(t('admin.accounts.dataImportCompletedWithErrors', msgParams))
    } else {
      appStore.showSuccess(t('admin.accounts.dataImportSuccess', msgParams))
      emit('imported')
    }
  } catch (error: any) {
    if (error instanceof SyntaxError) {
      appStore.showError(t('admin.accounts.dataImportParseFailed'))
    } else if (error?.message === STRUCTURED_EXPORT_ONLY_ERROR) {
      appStore.showError(t('admin.accounts.dataImportFailed'))
    } else {
      appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
    }
  } finally {
    importing.value = false
  }
}
</script>
