import { describe, expect, it } from 'vitest'

import { resolveGroupPricingModel } from '../lib/model-helpers'
import type { PricingModel } from '../types'

function baseModel(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'gpt-group-fixture',
    quota_type: 0,
    model_ratio: 10,
    completion_ratio: 2,
    cache_ratio: 0.5,
    enable_groups: ['default', 'vip'],
    ...overrides,
  }
}

describe('resolveGroupPricingModel', () => {
  it('returns the same model when the group has no override', () => {
    const model = baseModel({
      group_pricing: {
        vip: { quota_type: 0, model_ratio: 20, completion_ratio: 3 },
      },
    })
    expect(resolveGroupPricingModel(model, 'default')).toBe(model)
    expect(resolveGroupPricingModel(model, 'unknown')).toBe(model)
  })

  it('returns the same model when the model has no group pricing at all', () => {
    const model = baseModel()
    expect(resolveGroupPricingModel(model, 'vip')).toBe(model)
  })

  it('replaces base pricing fields with the group override', () => {
    const model = baseModel({
      group_pricing: {
        vip: {
          quota_type: 0,
          model_ratio: 20,
          completion_ratio: 3,
          cache_ratio: 0.25,
        },
      },
    })
    const resolved = resolveGroupPricingModel(model, 'vip')
    expect(resolved).not.toBe(model)
    expect(resolved.model_ratio).toBe(20)
    expect(resolved.completion_ratio).toBe(3)
    expect(resolved.cache_ratio).toBe(0.25)
    expect(resolved.model_name).toBe('gpt-group-fixture')
    // The original model is untouched.
    expect(model.model_ratio).toBe(10)
  })

  it('carries the group expression and billing mode for tiered overrides', () => {
    const model = baseModel({
      group_pricing: {
        vip: {
          quota_type: 0,
          model_ratio: 20,
          completion_ratio: 3,
          billing_mode: 'tiered_expr',
          billing_expr: 'tier("vip", p * 1)',
        },
      },
    })
    const resolved = resolveGroupPricingModel(model, 'vip')
    expect(resolved.billing_mode).toBe('tiered_expr')
    expect(resolved.billing_expr).toBe('tier("vip", p * 1)')
  })

  it('switches the group to fixed pricing when the override sets a price', () => {
    const model = baseModel({
      group_pricing: {
        vip: { quota_type: 1, model_ratio: 0, completion_ratio: 1, model_price: 0.5 },
      },
    })
    const resolved = resolveGroupPricingModel(model, 'vip')
    expect(resolved.quota_type).toBe(1)
    expect(resolved.model_price).toBe(0.5)
  })
})
