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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ModelDetailsContent } from '../components/model-details'
import type { PricingModel } from '../types'

// The performance/API tabs pull in chart and network dependencies that are
// irrelevant to pricing display.
vi.mock('../components/model-details-performance', () => ({
  ModelDetailsPerformance: () => null,
}))
vi.mock('../components/model-details-api', () => ({
  ModelDetailsApi: () => null,
}))

function haikuModel(): PricingModel {
  return {
    id: 1,
    model_name: 'claude-4-5-haiku',
    quota_type: 1,
    model_ratio: 0,
    completion_ratio: 0,
    model_price: 0.005,
    enable_groups: ['default', 'vip'],
    billing_mode: 'tiered_expr',
    billing_expr: 'tier("request", fixed(0.005))',
    group_pricing: {
      default: {
        quota_type: 1,
        model_ratio: 0,
        model_price: 0.005,
        completion_ratio: 0,
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("base", p * 3 + c * 15)',
      },
    },
  }
}

function renderDetails(model: PricingModel) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ModelDetailsContent
        model={model}
        groupRatio={{ default: 1, vip: 1 }}
        usableGroup={{
          default: { desc: '', ratio: 1 },
          vip: { desc: '', ratio: 1 },
        }}
        endpointMap={{}}
        autoGroups={[]}
        priceRate={1}
        usdExchangeRate={1}
        tokenUnit='M'
      />
    </QueryClientProvider>
  )
}

describe('model details group pricing display', () => {
  it('shows the group override expression prices instead of placeholder dashes', async () => {
    renderDetails(haikuModel())
    // The default group override bills per token ($3 input / $15 output per
    // 1M), while the base expression is a fixed per-request price. Before the
    // fix, the override group's cells fell back to '-' because the columns
    // followed the base expression's fields.
    const badges = await screen.findAllByText('default')
    const overrideCard = badges
      .map((badge) => badge.closest('div.overflow-hidden.rounded-lg.border'))
      .find((card) => card?.textContent?.includes('base'))
    expect(overrideCard).toBeTruthy()
    expect(overrideCard?.textContent).toContain('$3')
    expect(overrideCard?.textContent).toContain('$15')
  })

  it('keeps the base expression price for groups without an override', async () => {
    renderDetails(haikuModel())
    const badges = await screen.findAllByText('vip')
    const baseCard = badges
      .map((badge) => badge.closest('div.overflow-hidden.rounded-lg.border'))
      .find((card) => card?.textContent?.includes('request'))
    expect(baseCard).toBeTruthy()
    expect(baseCard?.textContent).toContain('$0.005')
  })
})
