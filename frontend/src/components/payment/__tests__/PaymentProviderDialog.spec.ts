import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import PaymentProviderDialog from '@/components/payment/PaymentProviderDialog.vue'

const listSepayBankAccountsMock = vi.hoisted(() => vi.fn())

function findButtonByText(wrapper: VueWrapper, text: string) {
  const button = wrapper.findAll('button').find(candidate => candidate.text() === text)
  if (!button) throw new Error(`button not found: ${text}`)
  return button
}

const messages: Record<string, string> = {
  'admin.settings.payment.providerConfig': 'Credentials',
  'admin.settings.payment.paymentGuideTrigger': 'View payment guide',
  'admin.settings.payment.field_bankAccountId': 'SePay bank account',
  'admin.settings.payment.field_apiToken': 'SePay API Token',
  'admin.settings.payment.field_webhookApiKey': 'SePay Webhook API Key',
  'admin.settings.payment.sepayBankAccountPlaceholder': 'Select bank account',
  'admin.settings.payment.sepayBankAccountHint': 'Load accounts from SePay.',
  'admin.settings.payment.sepayGuideSummary': 'SePay needs API token for bank account discovery.',
  'admin.settings.payment.sepayGuideNote': 'SePay webhook note.',
  'admin.settings.payment.sepayGuideApiTokenTitle': 'API Token',
  'admin.settings.payment.sepayGuideApiTokenOpen': 'Open SePay',
  'admin.settings.payment.sepayGuideApiTokenCall': 'Call list accounts',
  'admin.settings.payment.sepayGuideApiTokenFallback': 'Auto select single account',
  'admin.settings.payment.sepayGuideWebhookTitle': 'Webhook',
  'admin.settings.payment.sepayGuideWebhookOpen': 'Set callback URL',
  'admin.settings.payment.sepayGuideWebhookCall': 'Verify webhook',
  'admin.settings.payment.sepayGuideWebhookFallback': 'Fallback matching',
  'admin.settings.payment.sepayLoadBankAccounts': 'Load accounts',
  'admin.settings.payment.sepayGenerateWebhookApiKey': 'Generate webhook key',
  'admin.settings.payment.sepayWebhookApiKeyHint': 'Generate in sub2api and paste into SePay.',
  'admin.settings.payment.sepayWebhookTokenGenerationFailed': 'No crypto',
  'admin.settings.payment.sepayStoredBankAccount': 'Stored account #{id}',
  'admin.settings.payment.alipayGuideSummary': 'Desktop prefers QR precreate and falls back to cashier; mobile prefers WAP checkout.',
  'admin.settings.payment.wxpayGuideSummary': 'Desktop prefers Native QR; mobile routes to JSAPI or H5 based on browser context.',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      let value = messages[key] ?? key
      for (const [paramKey, paramValue] of Object.entries(params || {})) {
        value = value.replace(`{${paramKey}}`, String(paramValue))
      }
      return value
    },
  }),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    listSepayBankAccounts: listSepayBankAccountsMock,
  },
}))

function mountDialog(props: Partial<InstanceType<typeof PaymentProviderDialog>['$props']> = {}) {
  return mount(PaymentProviderDialog, {
    props: {
      show: true,
      saving: false,
      editing: null,
      allKeyOptions: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'sepay', label: 'SePay' },
        { value: 'stripe', label: 'Stripe' },
        { value: 'paddle', label: 'Paddle' },
      ],
      enabledKeyOptions: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
      ],
      allPaymentTypes: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'sepay', label: 'SePay' },
        { value: 'stripe', label: 'Stripe' },
        { value: 'paddle', label: 'Paddle' },
      ],
      redirectLabel: 'Redirect',
      ...props,
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>',
        },
        Select: {
          props: ['modelValue', 'options', 'disabled'],
          emits: ['update:modelValue', 'change'],
          template: '<div class="select-stub"><span v-for="option in options" :key="option.value">{{ option.label }}</span></div>',
        },
        ToggleSwitch: {
          template: '<div />',
        },
      },
    },
  })
}

describe('PaymentProviderDialog payment guide', () => {
  it('shows no payment guide for providers without a flow guide', () => {
    const wrapper = mountDialog()

    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.alipayGuideSummary'])
    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.wxpayGuideSummary'])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(false)
  })

  it.each([
    ['alipay', 'admin.settings.payment.alipayGuideSummary'],
    ['wxpay', 'admin.settings.payment.wxpayGuideSummary'],
    ['sepay', 'admin.settings.payment.sepayGuideSummary'],
  ])('shows the payment guide summary for %s', async (providerKey, summaryKey) => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset(providerKey)
    await nextTick()

    expect(wrapper.text()).toContain(messages[summaryKey])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(true)
  })
})


describe('PaymentProviderDialog SePay bank account selector', () => {
  it('loads bank accounts from SePay and renders human-readable account labels', async () => {
    listSepayBankAccountsMock.mockResolvedValueOnce({
      data: [
        {
          id: '123',
          bank_short_name: 'Vietcombank',
          account_number: '0071000888888',
          account_holder_name: 'NGUYEN VAN A',
          label: 'Vietcombank · 0071000888888 · NGUYEN VAN A',
        },
      ],
    })
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('sepay')
    await nextTick()
    await wrapper.find('input[type="password"]').setValue('sepay-token')
    await findButtonByText(wrapper, 'Load accounts').trigger('click')
    await flushPromises()
    await nextTick()

    expect(listSepayBankAccountsMock).toHaveBeenCalledWith({ apiToken: 'sepay-token' })
    expect(wrapper.text()).toContain('Vietcombank · 0071000888888 · NGUYEN VAN A')
  })

  it('loads bank accounts with stored server token while editing an existing SePay provider', async () => {
    listSepayBankAccountsMock.mockResolvedValueOnce({ data: [] })
    const wrapper = mountDialog({
      editing: {
        id: 42,
        provider_key: 'sepay',
        name: 'SePay',
        config: { bankAccountId: '123' },
        supported_types: ['sepay'],
        enabled: true,
        payment_mode: 'qrcode',
        refund_enabled: false,
        allow_user_refund: false,
        limits: '',
        sort_order: 0,
      },
    })

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('sepay')
    await nextTick()
    await findButtonByText(wrapper, 'Load accounts').trigger('click')
    await flushPromises()

    expect(listSepayBankAccountsMock).toHaveBeenCalledWith({ providerId: 42 })
  })

  it('generates a webhook API key for copying into SePay webhook settings', async () => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('sepay')
    await nextTick()
    await findButtonByText(wrapper, 'Generate webhook key').trigger('click')
    await nextTick()

    const values = wrapper.findAll('input').map(input => (input.element as HTMLInputElement).value)
    expect(values.some(value => /^vcwh_[0-9a-f]{64}$/.test(value))).toBe(true)
    expect(wrapper.text()).toContain('Generate in sub2api and paste into SePay.')
  })
})
