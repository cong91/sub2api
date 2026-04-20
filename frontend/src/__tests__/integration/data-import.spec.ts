import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'

const { showError, showSuccess, importDataMock } = vi.hoisted(() => ({
  showError: vi.fn(),
  showSuccess: vi.fn(),
  importDataMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      importData: importDataMock
    }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const mountModal = () => mount(ImportDataModal, {
  props: { show: true },
  global: {
    stubs: {
      BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
    }
  }
})

const attachFiles = async (wrapper: ReturnType<typeof mountModal>, files: File[]) => {
  const input = wrapper.find('input[type="file"]')
  Object.defineProperty(input.element, 'files', {
    configurable: true,
    value: files
  })
  await input.trigger('change')
}

describe('ImportDataModal', () => {
  beforeEach(() => {
    showError.mockReset()
    showSuccess.mockReset()
    importDataMock.mockReset()
  })

  it('未选择文件时提示错误', async () => {
    const wrapper = mountModal()

    await wrapper.find('form').trigger('submit')
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')
  })

  it('无效 JSON 时提示解析失败', async () => {
    const wrapper = mountModal()
    const file = new File(['invalid json'], 'data.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve('invalid json')
    })

    await attachFiles(wrapper, [file])
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailed')
  })

  it('支持一次选择多个 CPA 文件并合并导入结果', async () => {
    importDataMock
      .mockResolvedValueOnce({
        proxy_created: 0,
        proxy_reused: 0,
        proxy_failed: 0,
        account_created: 2,
        account_failed: 0,
        errors: []
      })

    const wrapper = mountModal()
    const first = new File(['{}'], 'CPA-1.json', { type: 'application/json' })
    const second = new File(['{}'], 'CPA-2.json', { type: 'application/json' })

    Object.defineProperty(first, 'text', {
      value: () => Promise.resolve(JSON.stringify({ refresh_token: 'rt-1', email: 'first@example.com' }))
    })
    Object.defineProperty(second, 'text', {
      value: () => Promise.resolve(JSON.stringify({ refresh_token: 'rt-2', email: 'second@example.com' }))
    })

    await attachFiles(wrapper, [first, second])
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(importDataMock).toHaveBeenCalledTimes(1)
    expect(importDataMock).toHaveBeenCalledWith({
      data: [
        { refresh_token: 'rt-1', email: 'first@example.com' },
        { refresh_token: 'rt-2', email: 'second@example.com' }
      ],
      skip_default_group_bind: true
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.dataImportSuccess')
    expect(wrapper.emitted('imported')).toBeTruthy()
  })

  it('chặn chọn nhiều file khi có file export chuẩn sub2api', async () => {
    const wrapper = mountModal()
    const exportFile = new File(['{}'], 'sub2api-account-data-20260421.json', { type: 'application/json' })
    const cpaFile = new File(['{}'], 'CPA.json', { type: 'application/json' })

    await attachFiles(wrapper, [exportFile, cpaFile])

    expect(showError).toHaveBeenCalledWith('Structured export file must be imported alone')
    expect(importDataMock).not.toHaveBeenCalled()
  })
})
