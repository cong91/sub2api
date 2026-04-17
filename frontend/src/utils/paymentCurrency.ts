export function normalizeCurrencyCode(code?: string | null, fallback = 'USD'): string {
  const normalized = (code || '').trim().toUpperCase()
  return normalized || fallback
}

export function currencySymbol(code?: string | null): string {
  switch (normalizeCurrencyCode(code)) {
    case 'CNY':
      return '¥'
    case 'USD':
      return '$'
    case 'VND':
      return '₫'
    default:
      return ''
  }
}

export function formatCurrencyAmount(amount: number, code?: string | null, digits = 2): string {
  const normalized = normalizeCurrencyCode(code)
  const symbol = currencySymbol(normalized)
  const formatted = Number.isFinite(amount) ? amount.toFixed(digits) : '0.00'
  if (symbol) {
    return `${symbol}${formatted}`
  }
  return `${formatted} ${normalized}`
}
