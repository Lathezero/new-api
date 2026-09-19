/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createJSONStorage } from 'zustand/middleware'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { CachedPriceCell } from '../components/cached-price-cell'
import { ModelCard } from '../components/model-card'
import { ModelPriceCell } from '../components/model-price-cell'
import type { PricingModel } from '../types'

// Summary surfaces (card grid + pricing table) must show the selected
// group's override pricing instead of the global pricing when a
// (model, group) override is configured. The group ratio multiplier
// still applies on top, matching how the group is billed.

function summaryModel(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'example-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 3,
    enable_groups: ['default', 'premium'],
    group_ratio: { default: 1, premium: 3 },
    ...overrides,
  }
}

const originalStorage = useSystemConfigStore.persist.getOptions().storage
beforeEach(() => {
  const storage = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
    clear: () => storage.clear(),
    key: (index: number) => [...storage.keys()][index] ?? null,
    get length() {
      return storage.size
    },
  })
  useSystemConfigStore.persist.setOptions({
    storage: createJSONStorage(() => localStorage),
  })
  useSystemConfigStore.getState().setConfig({
    currency: { ...DEFAULT_CURRENCY_CONFIG, quotaDisplayType: 'USD' },
  })
})
afterEach(() => {
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  vi.unstubAllGlobals()
  useSystemConfigStore.persist.setOptions({ storage: originalStorage })
})

describe('summary prices with group pricing overrides', () => {
  it('applies a group ratio override to card prices only when that group is selected', () => {
    const model = summaryModel({
      group_pricing: {
        premium: {
          quota_type: 0,
          model_ratio: 5,
          completion_ratio: 1,
          billing_mode: 'ratio',
        },
      },
    })
    const { rerender } = render(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='premium' />
    )
    // Override ratios (5 / 1) instead of the global ones (1 / 3),
    // still multiplied by the premium group ratio (3).
    expect(screen.getByText('Input').parentElement).toHaveTextContent(
      /\$30\s*\/\s*1M/
    )
    expect(screen.getByText('Output').parentElement).toHaveTextContent(
      /\$30\s*\/\s*1M/
    )

    rerender(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='default' />
    )
    expect(screen.getByText('Input').parentElement).toHaveTextContent(
      /\$2\s*\/\s*1M/
    )
    expect(screen.getByText('Output').parentElement).toHaveTextContent(
      /\$6\s*\/\s*1M/
    )

    // No selection keeps the best-price heuristic on the global pricing.
    rerender(<ModelCard model={model} onClick={vi.fn()} />)
    expect(screen.getByText('Input').parentElement).toHaveTextContent(
      /\$2\s*\/\s*1M/
    )
  })

  it('switches the card to per-request pricing when the selected group override sets a fixed price', () => {
    const model = summaryModel({
      group_pricing: {
        premium: {
          quota_type: 1,
          model_ratio: 0,
          completion_ratio: 0,
          model_price: 0.5,
          billing_mode: 'ratio',
        },
      },
    })
    render(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='premium' />
    )
    // 0.5 USD per request × group ratio 3.
    expect(screen.getByText(/\$1\.5/)).toHaveTextContent(
      /\$1\.5\s*\/\s*request/
    )
    expect(screen.queryByText('Input')).not.toBeInTheDocument()
  })

  it('shows the group override expression on cards when the selected group has one', () => {
    const model = summaryModel({
      billing_mode: 'tiered_expr',
      billing_expr: 'tier("base", p * 3 + c * 15)',
      group_pricing: {
        default: {
          quota_type: 0,
          model_ratio: 0,
          completion_ratio: 0,
          billing_mode: 'tiered_expr',
          billing_expr: 'tier("base", p * 10 + c * 20)',
        },
      },
    })
    const { rerender } = render(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='default' />
    )
    expect(screen.getByText('Input').parentElement).toHaveTextContent(
      /\$10\s*\/\s*1M/
    )
    expect(screen.getByText('Output').parentElement).toHaveTextContent(
      /\$20\s*\/\s*1M/
    )

    // Groups without an override keep the base expression.
    rerender(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='premium' />
    )
    expect(screen.getByText('Input').parentElement).toHaveTextContent(
      /\$9\s*\/\s*1M/
    )
    expect(screen.getByText('Output').parentElement).toHaveTextContent(
      /\$45\s*\/\s*1M/
    )
  })

  it('shows ratio pricing instead of the unconfigured notice when a usage-based model has a group ratio override', () => {
    const model = summaryModel({
      model_ratio: 0,
      completion_ratio: 0,
      billing_usage_schema: { seconds: { unit: 'second' } },
      group_pricing: {
        premium: {
          quota_type: 0,
          model_ratio: 2,
          completion_ratio: 2,
          billing_mode: 'ratio',
        },
      },
    })
    const { rerender } = render(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='default' />
    )
    expect(
      screen.getByText('Usage-based billing · price not configured')
    ).toBeVisible()

    // The ratio override suppresses the usage-based expression at billing
    // time, so the group is charged by token ratio and the card says so.
    rerender(
      <ModelCard model={model} onClick={vi.fn()} selectedGroup='premium' />
    )
    expect(
      screen.queryByText('Usage-based billing · price not configured')
    ).not.toBeInTheDocument()
    expect(screen.getByText('Input').parentElement).toHaveTextContent(
      /\$12\s*\/\s*1M/
    )
    expect(screen.getByText('Output').parentElement).toHaveTextContent(
      /\$24\s*\/\s*1M/
    )
  })

  it('shows the override cache price in table cells only for the configured group', () => {
    const model = summaryModel({
      cache_ratio: null,
      group_pricing: {
        premium: {
          quota_type: 0,
          model_ratio: 1,
          completion_ratio: 1,
          cache_ratio: 0.5,
          billing_mode: 'ratio',
        },
      },
    })
    const { rerender } = render(
      <CachedPriceCell model={model} options={{ selectedGroup: 'premium' }} />
    )
    // 1 × 2 × cache 0.5 × group ratio 3 = $3 per 1M.
    expect(screen.getByText(/\$3/)).toBeVisible()

    rerender(
      <CachedPriceCell model={model} options={{ selectedGroup: 'default' }} />
    )
    expect(screen.queryByText(/\$3/)).not.toBeInTheDocument()
  })

  it('applies group overrides to table price cells', () => {
    const model = summaryModel({
      group_pricing: {
        premium: {
          quota_type: 1,
          model_ratio: 0,
          completion_ratio: 0,
          model_price: 0.5,
          billing_mode: 'ratio',
        },
      },
    })
    const view = render(
      <ModelPriceCell model={model} options={{ selectedGroup: 'premium' }} />
    )
    expect(view.container).toHaveTextContent('Per-request')
    expect(view.container).toHaveTextContent('1.5')
    expect(view.container).not.toHaveTextContent('Input')

    view.rerender(
      <ModelPriceCell model={model} options={{ selectedGroup: 'default' }} />
    )
    expect(view.container).toHaveTextContent('Input')
    expect(screen.getByText('Input').parentElement).toHaveTextContent('2')
    expect(screen.getByText('Output').parentElement).toHaveTextContent('6')
  })
})
