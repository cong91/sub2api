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

  it('hỗ trợ import nhiều file json trong một lần', async () => {
    const importData = vi.mocked(adminAPI.accounts.importData)
    importData.mockResolvedValue({
      proxy_created: 0,
      proxy_reused: 0,
      proxy_failed: 0,
      account_created: 2,
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
    const exportedFile = new File(['exported'], 'sub2api-account.json', { type: 'application/json' })
    Object.defineProperty(exportedFile, 'text', {
      value: () => Promise.resolve(JSON.stringify({
        exported_at: '2026-04-08T07:43:55Z',
        proxies: [],
        accounts: [
          {
            name: 'exported@example.com',
            platform: 'openai',
            type: 'oauth',
            credentials: { access_token: 'at-exported' },
            concurrency: 10,
            priority: 1,
            auto_pause_on_expired: true
          }
        ]
      }))
    })

    const tokenFile = new File(['token'], 'token_xxx.json', { type: 'application/json' })
    Object.defineProperty(tokenFile, 'text', {
      value: () => Promise.resolve(JSON.stringify({
        access_token: 'at-token',
        refresh_token: 'rt-token',
        account_id: 'acct-002',
        email: 'token@example.com',
        type: 'codex'
      }))
    })

    Object.defineProperty(input.element, 'files', {
      value: [exportedFile, tokenFile]
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
            name: 'exported@example.com',
            platform: 'openai',
            type: 'oauth',
            credentials: { access_token: 'at-exported' },
            concurrency: 10,
            priority: 1,
            auto_pause_on_expired: true
          },
          {
            name: 'token@example.com',
            platform: 'openai',
            type: 'oauth',
            credentials: {
              access_token: 'at-token',
              refresh_token: 'rt-token',
              email: 'token@example.com',
              account_id: 'acct-002',
              chatgpt_account_id: 'acct-002'
            },
            extra: undefined,
            concurrency: 10,
            priority: 1,
            auto_pause_on_expired: true
          }
        ]
      },
      skip_default_group_bind: true
    })
  })
})
