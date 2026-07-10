import { describe, expect, it } from 'vitest'

import {
  BILLING_MODE_IMAGE,
  BILLING_MODE_VIDEO,
  getDisplayBillingMode,
  isImageUsage,
  isVideoUsage,
  videoSecondUnitPrice,
} from '../billingMode'

describe('billing mode media compatibility', () => {
  it('prioritizes video metadata over the legacy image counter', () => {
    const row = {
      billing_mode: BILLING_MODE_VIDEO,
      image_count: 1,
      video_count: 1,
      total_cost: 1.12,
    }

    expect(getDisplayBillingMode(row)).toBe(BILLING_MODE_VIDEO)
    expect(isVideoUsage(row)).toBe(true)
    expect(isImageUsage(row)).toBe(false)
  })

  it('recognizes historical image rows without a billing mode', () => {
    const row = { billing_mode: null, image_count: 2, total_cost: 0.2 }

    expect(getDisplayBillingMode(row)).toBe(BILLING_MODE_IMAGE)
    expect(isImageUsage(row)).toBe(true)
    expect(isVideoUsage(row)).toBe(false)
  })

  it('calculates the per-second video price across all generated videos', () => {
    expect(videoSecondUnitPrice({ video_count: 2, video_duration_seconds: 8, total_cost: 2.24 })).toBeCloseTo(0.14)
  })
})
