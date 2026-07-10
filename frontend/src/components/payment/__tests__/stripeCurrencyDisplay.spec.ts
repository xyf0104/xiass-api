import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const inlineSource = readFileSync(resolve(dir, '../StripePaymentInline.vue'), 'utf8')
const popupSource = readFileSync(resolve(dir, '../../../views/user/StripePopupView.vue'), 'utf8')

describe('Stripe popup currency display', () => {
  it('passes the order currency to the popup route', () => {
    expect(inlineSource).toContain("currency: props.currency || 'CNY'")
  })

  it('formats the popup amount using the order currency', () => {
    expect(popupSource).toContain('formatPaymentAmount(Number(amount), currency)')
    expect(popupSource).not.toContain('>¥{{ amount }}<')
  })
})
