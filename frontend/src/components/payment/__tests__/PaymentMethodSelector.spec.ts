import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (_key: string, fallback?: string) => fallback ?? _key,
  }),
}))

describe('PaymentMethodSelector', () => {
  it('shows the configured display name for custom EasyPay methods', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'ldc',
        methods: [{ type: 'ldc', display_name: 'LDC Pay', fee_rate: 0, available: true }],
      },
    })

    expect(wrapper.text()).toContain('LDC Pay')
    expect(wrapper.text()).not.toContain('ldc')
    expect(wrapper.text()).not.toContain('payment.methods.ldc')
  })

  it('uses the generic selected style for custom methods that contain built-in names', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'card_alipay',
        methods: [{ type: 'card_alipay', display_name: 'Card Pay', fee_rate: 0, available: true }],
      },
    })

    const button = wrapper.get('button')
    expect(button.classes()).toContain('border-primary-500')
    expect(button.classes()).not.toContain('border-[#02A9F1]')
  })

  it('uses dedicated Paddle and SePay icons instead of falling back to Alipay', () => {
    const wrapper = mount(PaymentMethodSelector, {
      props: {
        selected: 'paddle',
        methods: [
          { type: 'paddle', fee_rate: 0, available: true },
          { type: 'sepay', fee_rate: 0, available: true },
          { type: 'alipay', fee_rate: 0, available: true },
        ],
      },
    })

    const icons = wrapper.findAll('img')
    const srcByAlt = Object.fromEntries(
      icons.map(icon => [icon.attributes('alt'), icon.attributes('src')]),
    )

    expect(srcByAlt.paddle).toContain('paddle')
    expect(srcByAlt.sepay).toContain('sepay')
    expect(srcByAlt.paddle).not.toBe(srcByAlt.alipay)
    expect(srcByAlt.sepay).not.toBe(srcByAlt.alipay)
  })
})
