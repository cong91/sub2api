import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

const { getModelsListCandidates, syncUpstreamModels } = vi.hoisted(() => ({
  getModelsListCandidates: vi.fn(),
  syncUpstreamModels: vi.fn()
}))

const appStore = vi.hoisted(() => ({
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore
}))

vi.mock('@/api/admin/groups', () => ({
  getModelsListCandidates
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key)
    })
  }
})

vi.mock('@/components/common/ModelIcon.vue', () => ({
  default: { template: '<span />' }
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span />' }
}))

describe('ModelWhitelistSelector catalog-only options', () => {
  beforeEach(() => {
    getModelsListCandidates.mockReset().mockResolvedValue(['claude-opus-5'])
    syncUpstreamModels.mockReset().mockResolvedValue({ models: ['claude-opus-5', 'customer-custom-model'] })
    appStore.showError.mockReset()
    appStore.showSuccess.mockReset()
    appStore.showInfo.mockReset()
  })

  it('does not render a custom model input outside the catalog selector', async () => {
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'anthropic'
      }
    })
    await flushPromises()

    expect(wrapper.find('input[placeholder="admin.accounts.enterCustomModelName"]').exists()).toBe(false)
  })

  it('filters upstream sync results through catalog candidates before emitting', async () => {
    const onUpdate = vi.fn()
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'anthropic',
        accountId: 42,
        'onUpdate:modelValue': onUpdate
      }
    })
    await flushPromises()

    const syncButton = wrapper.findAll('button').find(button => button.text().includes('admin.accounts.syncUpstreamModels'))
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(onUpdate).toHaveBeenCalledWith(['claude-opus-5'])
  })

  it('removes preloaded custom selections after catalog candidates load', async () => {
    const onUpdate = vi.fn()
    mount(ModelWhitelistSelector, {
      props: {
        modelValue: ['customer-custom-model', 'claude-opus-5'],
        platform: 'anthropic',
        'onUpdate:modelValue': onUpdate
      }
    })

    await flushPromises()

    expect(onUpdate).toHaveBeenCalledWith(['claude-opus-5'])
  })
})
