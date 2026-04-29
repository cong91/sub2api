import { describe, expect, it } from 'vitest'
import { PROVIDER_CONFIG_FIELDS } from '@/components/payment/providerConfig'

function findWxpayField(key: string) {
  const fields = PROVIDER_CONFIG_FIELDS.wxpay || []
  return fields.find(field => field.key === key)
}

function findSepayField(key: string) {
  const fields = PROVIDER_CONFIG_FIELDS.sepay || []
  return fields.find(field => field.key === key)
}

describe('PROVIDER_CONFIG_FIELDS.wxpay', () => {
  it('keeps admin form validation aligned with backend-required credentials', () => {
    expect(findWxpayField('publicKeyId')?.optional).toBeFalsy()
    expect(findWxpayField('certSerial')?.optional).toBeFalsy()
  })

  it('only keeps the simplified visible credential set in the admin form', () => {
    expect(findWxpayField('mpAppId')).toBeUndefined()
    expect(findWxpayField('h5AppName')).toBeUndefined()
    expect(findWxpayField('h5AppUrl')).toBeUndefined()
  })
})

describe('PROVIDER_CONFIG_FIELDS.sepay', () => {
  it('requires SePay API credentials while keeping bank account ID user-friendly', () => {
    expect(findSepayField('apiToken')?.optional).toBeFalsy()
    expect(findSepayField('webhookApiKey')?.optional).toBeFalsy()
    expect(findSepayField('webhookApiKey')?.sensitive).toBe(true)
    expect(findSepayField('bankAccountId')?.optional).toBe(true)
  })
})
