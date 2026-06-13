import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'
import { adminAPI } from '@/api/admin'

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
    },
    proxies: {
      testProxy: vi.fn()
    }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const activeGroup = {
  id: 2,
  name: 'OpenAI tag',
  description: null,
  platform: 'openai' as const,
  rate_multiplier: 1,
  is_exclusive: false,
  status: 'active' as const,
  subscription_type: 'standard' as const,
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: false,
  sort_order: 1
}

const secondaryGroup = {
  ...activeGroup,
  id: 3,
  name: 'Claude tag',
  sort_order: 2
}

const activeProxy = {
  id: 4,
  name: 'Live proxy',
  protocol: 'http' as const,
  host: '127.0.0.1',
  port: 8080,
  username: null,
  password: null,
  status: 'active' as const,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
}

const mountModal = (props: Record<string, unknown> = {}) => mount(ImportDataModal, {
  props: {
    show: true,
    groups: [activeGroup, secondaryGroup],
    proxies: [activeProxy],
    ...props
  },
  global: {
    stubs: {
      BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
      GroupBadge: { props: ['name'], template: '<span class="group-badge-stub">{{ name }}</span>' },
      ProxySelector: {
        props: ['modelValue', 'proxies'],
        emits: ['update:modelValue'],
        template: '<button class="proxy-selector-stub" type="button" @click="$emit(\'update:modelValue\', proxies[0]?.id ?? null)">proxy</button>'
      }
    }
  }
})

const selectFiles = (wrapper: ReturnType<typeof mountModal>, files: File[]) => {
  const vm = wrapper.vm as unknown as { handleFileChange: (event: Event) => void }
  const input = document.createElement('input')
  Object.defineProperty(input, 'files', {
    value: files
  })
  vm.handleFileChange({ target: input } as unknown as Event)
}

describe('ImportDataModal', () => {
  beforeEach(() => {
    showError.mockReset()
    showSuccess.mockReset()
    vi.mocked(adminAPI.accounts.importData).mockReset()
  })

  it('未选择文件时提示错误', async () => {
    const wrapper = mountModal()

    await wrapper.find('form').trigger('submit')
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')
  })

  it('无效 JSON 时提示解析失败', async () => {
    const wrapper = mountModal()

    const vm = wrapper.vm as unknown as { handleFileChange: (event: Event) => void; handleImport: () => Promise<void> }
    const file = new File(['invalid json'], 'data.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve('invalid json')
    })
    const input = document.createElement('input')
    Object.defineProperty(input, 'files', {
      value: [file]
    })

    vm.handleFileChange({ target: input } as unknown as Event)
    await vm.handleImport()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailed')
  })

  it('hiển thị group tag để chọn nhiều nhóm và gửi group_ids/proxy assignment trong payload import', async () => {
    vi.mocked(adminAPI.accounts.importData).mockResolvedValue({
      proxy_created: 0,
      proxy_reused: 0,
      proxy_failed: 0,
      account_created: 1,
      account_failed: 0,
      errors: []
    })
    const wrapper = mountModal()
    expect(wrapper.text()).toContain('OpenAI tag')
    expect(wrapper.text()).toContain('Claude tag')

    const groupInputs = wrapper.findAll('input[type="checkbox"]')
    await groupInputs[0].setValue(true)
    await groupInputs[1].setValue(true)
    const defaultLiveInput = wrapper.find('input[value="default_live"]')
    await defaultLiveInput.setValue(true)
    await wrapper.find('.proxy-selector-stub').trigger('click')

    const file = new File([JSON.stringify({
      exported_at: '2026-01-01T00:00:00Z',
      proxies: [],
      accounts: [{
        name: 'acc',
        platform: 'openai',
        type: 'oauth',
        credentials: { token: '[REDACTED]' },
        concurrency: 1,
        priority: 1
      }]
    })], 'data.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve(JSON.stringify({
        exported_at: '2026-01-01T00:00:00Z',
        proxies: [],
        accounts: [{
          name: 'acc',
          platform: 'openai',
          type: 'oauth',
          credentials: { token: '[REDACTED]' },
          concurrency: 1,
          priority: 1
        }]
      }))
    })
    selectFiles(wrapper, [file])

    await wrapper.find('form').trigger('submit')

    await vi.waitFor(() => {
      expect(adminAPI.accounts.importData).toHaveBeenCalledWith(expect.objectContaining({
        group_ids: [2, 3],
        skip_default_group_bind: true,
        proxy_assignment: {
          mode: 'default_live',
          default_proxy_id: 4
        }
      }))
    })
    const request = vi.mocked(adminAPI.accounts.importData).mock.calls[0][0]
    expect(request.group_id).toBeUndefined()
  })
})
