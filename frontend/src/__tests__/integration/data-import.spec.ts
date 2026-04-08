import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { adminAPI } from '@/api/admin'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'

const showError = vi.fn()
const showSuccess = vi.fn()

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      importData: vi.fn()
    }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

describe('ImportDataModal', () => {
  beforeEach(() => {
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('未选择文件时提示错误', async () => {
    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    await wrapper.find('form').trigger('submit')
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')
  })

  it('无效 JSON 时提示解析失败', async () => {
    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    const input = wrapper.find('input[type="file"]')
    const file = new File(['invalid json'], 'data.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve('invalid json')
    })
    Object.defineProperty(input.element, 'files', {
      value: [file]
    })

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await Promise.resolve()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailed')
  })

  it('token_xxx.json kiểu codex được normalize thành openai oauth import payload', async () => {
    const importData = vi.mocked(adminAPI.accounts.importData)
    importData.mockResolvedValue({
      proxy_created: 0,
      proxy_reused: 0,
      proxy_failed: 0,
      account_created: 1,
      account_failed: 0,
      errors: []
    })

    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    const input = wrapper.find('input[type="file"]')
    const file = new File(['token json'], 'token_xxx.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve(JSON.stringify({
        access_token: 'at-123',
        refresh_token: 'rt-456',
        id_token: 'id-789',
        account_id: 'acct-001',
        email: 'user@example.com',
        expired: '2026-04-15T02:46:24Z',
        type: 'codex',
        meta: {
          mailbox_email: 'user@example.com',
          provider: 'microsoft'
        }
      }))
    })
    Object.defineProperty(input.element, 'files', {
      value: [file]
    })

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await Promise.resolve()

    expect(importData).toHaveBeenCalledWith({
      data: {
        exported_at: expect.any(String),
        proxies: [],
        accounts: [
          {
            name: 'user@example.com',
            platform: 'openai',
            type: 'oauth',
            credentials: {
              access_token: 'at-123',
              refresh_token: 'rt-456',
              id_token: 'id-789',
              email: 'user@example.com',
              account_id: 'acct-001',
              chatgpt_account_id: 'acct-001',
              expires_at: '2026-04-15T02:46:24Z'
            },
            extra: {
              mailbox_email: 'user@example.com',
              provider: 'microsoft'
            },
            concurrency: 10,
            priority: 1,
            auto_pause_on_expired: true
          }
        ]
      },
      skip_default_group_bind: true
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.dataImportSuccess')
  })
})
