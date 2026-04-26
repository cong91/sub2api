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
import type { AdminDataImportResult, AdminDataPayload } from '@/types'

type AdminImportData = AdminDataPayload

const IMPORT_FORMAT_FILE_PREFIX = 'sub2api-account'
const STRUCTURED_EXPORT_ONLY_ERROR = 'Structured export file must be imported alone'

const IMPORT_FORMAT_TYPE = 'sub2api-account-data'

interface ImportAggregateResult extends AdminDataImportResult {
  errors: NonNullable<AdminDataImportResult['errors']>
}

interface ParsedImportFile {
  data: AdminImportData
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

const normalizeCPAAccountsPayload = (accounts: Record<string, unknown>[]): AdminDataPayload => ({
  exported_at: new Date(0).toISOString(),
  proxies: [],
  accounts: accounts.map((account) => ({
    name: typeof account.name === 'string' ? account.name : (typeof account.account_id === 'string' ? account.account_id : 'Imported Account'),
    notes: typeof account.notes === 'string' ? account.notes : null,
    platform: typeof account.platform === 'string' ? account.platform as never : 'openai',
    type: typeof account.type === 'string' ? account.type as never : 'apikey',
    credentials: account,
    extra: {},
    proxy_key: typeof account.proxy_key === 'string' ? account.proxy_key : null,
    concurrency: typeof account.concurrency === 'number' ? account.concurrency : 1,
    priority: typeof account.priority === 'number' ? account.priority : 0,
    rate_multiplier: typeof account.rate_multiplier === 'number' ? account.rate_multiplier : null,
    expires_at: typeof account.expires_at === 'number' ? account.expires_at : null,
    auto_pause_on_expired: typeof account.auto_pause_on_expired === 'boolean' ? account.auto_pause_on_expired : false,
  })),
})

const parseImportPayload = (raw: unknown): AdminImportData => {
  if (Array.isArray(raw)) {
    if (raw.every(isCPAAccountObject)) {
      return normalizeCPAAccountsPayload(raw)
    }
    throw new SyntaxError('Unsupported import array format')
  }

  if (isStructuredDataPayload(raw)) {
    return raw as AdminImportData
  }

  if (isCPAAccountObject(raw)) {
    return normalizeCPAAccountsPayload([raw])
  }

  throw new SyntaxError('Unsupported import object format')
}

const parseImportFiles = (entries: ParsedImportFile[]): AdminImportData[] => {
  return entries.map((entry) => entry.data)
}

const parseImportFilesFromText = async (files: File[]): Promise<AdminImportData[]> => {
  const parsedEntries = await Promise.all(files.map(async (sourceFile) => {
    const text = await readFileAsText(sourceFile)
    const raw = JSON.parse(text) as unknown
    return {
      data: parseImportPayload(raw)
    } satisfies ParsedImportFile
  }))

  return parseImportFiles(parsedEntries)
}

const buildSelectedFilesLabel = (files: File[]): string => {
  if (files.length === 0) {
    return ''
  }
  if (files.length === 1) {
    return files[0].name
  }
  return `${files.length} JSON files selected`
}

const importDataPayloads = async (payloads: AdminImportData[]): Promise<ImportAggregateResult> => {
  const responses = await Promise.all(payloads.map((data) => adminAPI.accounts.importData({
    data,
    skip_default_group_bind: true
  })))

  return mergeImportResults(responses)
}

const resetInputValue = (input: HTMLInputElement | null) => {
  if (input) {
    input.value = ''
  }
}

const clearSelection = () => {
  files.value = []
  result.value = null
  resetInputValue(fileInput.value)
}

const isStructuredExportFilename = (fileName: string) => {
  const normalized = fileName.toLowerCase()
  return normalized.startsWith(IMPORT_FORMAT_FILE_PREFIX) || normalized.startsWith(IMPORT_FORMAT_TYPE)
}

const normalizeSelectedFiles = (fileList: FileList | null): File[] => {
  if (!fileList) {
    return []
  }

  const selected = Array.from(fileList)
  const hasStructuredDataFile = selected.some((file) => isStructuredExportFilename(file.name))
  if (hasStructuredDataFile && selected.length > 1) {
    throw new Error(STRUCTURED_EXPORT_ONLY_ERROR)
  }
  return selected
}

const isParseError = (error: unknown): error is SyntaxError => error instanceof SyntaxError

const getImportErrorMessage = (error: unknown, t: (key: string) => string): string => {
  if (isParseError(error)) {
    return t('admin.accounts.dataImportParseFailed')
  }
  return error instanceof Error ? error.message : t('admin.accounts.dataImportFailed')
}

const showImportToast = (
  result: ImportAggregateResult,
  showError: (message: string) => void,
  showSuccess: (message: string) => void,
  t: (key: string, params?: Record<string, unknown>) => string
) => {
  const msgParams: Record<string, unknown> = {
    account_created: result.account_created,
    account_failed: result.account_failed,
    proxy_created: result.proxy_created,
    proxy_reused: result.proxy_reused,
    proxy_failed: result.proxy_failed,
  }
  if (result.account_failed > 0 || result.proxy_failed > 0) {
    showError(t('admin.accounts.dataImportCompletedWithErrors', msgParams))
    return
  }
  showSuccess(t('admin.accounts.dataImportSuccess', msgParams))
}

const emitImportedIfSuccessful = (result: ImportAggregateResult, emit: Emits) => {
  if (result.account_failed === 0 && result.proxy_failed === 0) {
    emit('imported')
  }
}

const assignImportResult = (target: typeof result, value: ImportAggregateResult) => {
  target.value = value
}

const readAndImportSelectedFiles = async (selectedFiles: File[]): Promise<ImportAggregateResult> => {
  const payloads = await parseImportFilesFromText(selectedFiles)
  return importDataPayloads(payloads)
}

const ensureFilesSelected = (selectedFiles: File[], showError: (message: string) => void, t: (key: string) => string) => {
  if (selectedFiles.length === 0) {
    showError(t('admin.accounts.dataImportSelectFile'))
    throw new Error('No files selected')
  }
}

const shouldStopImporting = (error: unknown) => error instanceof Error && error.message === 'No files selected'

const finalizeImportState = (flag: typeof importing) => {
  flag.value = false
}

const startImportState = (flag: typeof importing) => {
  flag.value = true
}

const setSelectedFiles = (selectedFiles: File[]) => {
  files.value = selectedFiles
}

const selectedFilesLabel = computed(() => buildSelectedFilesLabel(files.value))

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const files = ref<File[]>([])
const result = ref<AdminDataImportResult | null>(null)

const fileInput = ref<HTMLInputElement | null>(null)

const errorItems = computed(() => result.value?.errors || [])

watch(
  () => props.show,
  (open) => {
    if (open) {
      clearSelection()
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement

  try {
    setSelectedFiles(normalizeSelectedFiles(target.files))
    result.value = null
  } catch (error) {
    resetInputValue(target)
    setSelectedFiles([])
    result.value = null
    appStore.showError(getImportErrorMessage(error, t))
  }
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

const handleImport = async () => {
  try {
    ensureFilesSelected(files.value, appStore.showError, t)
  } catch (error) {
    if (!shouldStopImporting(error)) {
      appStore.showError(getImportErrorMessage(error, t))
    }
    return
  }

  startImportState(importing)
  try {
    const aggregatedResult = await readAndImportSelectedFiles(files.value)
    assignImportResult(result, aggregatedResult)
    showImportToast(aggregatedResult, appStore.showError, appStore.showSuccess, t)
    emitImportedIfSuccessful(aggregatedResult, emit)
  } catch (error) {
    appStore.showError(getImportErrorMessage(error, t))
  } finally {
    finalizeImportState(importing)
  }
}
</script>
