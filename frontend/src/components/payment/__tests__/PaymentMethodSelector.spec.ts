import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PaymentMethodSelector from '../PaymentMethodSelector.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

describe('PaymentMethodSelector', () => {
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

    expect(srcByAlt['payment.methods.paddle']).toContain('paddle')
    expect(srcByAlt['payment.methods.sepay']).toContain('sepay')
    expect(srcByAlt['payment.methods.paddle']).not.toBe(srcByAlt['payment.methods.alipay'])
    expect(srcByAlt['payment.methods.sepay']).not.toBe(srcByAlt['payment.methods.alipay'])
  })
})
