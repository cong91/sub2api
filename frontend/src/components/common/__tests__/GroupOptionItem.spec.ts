import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import GroupOptionItem from '../GroupOptionItem.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key === 'common.rateMultiplier' ? 'Hệ số giá' : key
    })
  }
})

describe('GroupOptionItem', () => {
  it('renders the translated rate label together with the numeric multiplier', () => {
    const wrapper = mount(GroupOptionItem, {
      props: {
        name: 'GPT Group',
        platform: 'openai',
        rateMultiplier: 0.2
      },
      global: {
        stubs: {
          PlatformIcon: true
        }
      }
    })

    expect(wrapper.text()).toContain('Hệ số giá')
    expect(wrapper.text()).toContain('0.2x')
    expect(wrapper.text()).toContain('Hệ số giá: 0.2x')
  })
})
