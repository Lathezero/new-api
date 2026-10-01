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
import { fireEvent, render, screen } from '@testing-library/react'
import type { AxiosAdapter } from 'axios'
import { afterEach, assert, expect, it, vi } from 'vitest'

import { api } from '@/lib/http-client'
import { usePricingPreferencesStore } from '@/stores/pricing-preferences-store'

import { GroupPricingOverridesEditor } from '../group-pricing-overrides-editor'
import type { ModelPricingEditorPanelHandle } from '../model-pricing-sheet'

// Regression: the per-group editor built its editData inline on every render,
// so any parent re-render (e.g. the dirty report triggered by the edit itself)
// reset the sub-panel form and wiped the in-progress price; saving then
// reported "No model price changes to save".

const originalAdapter = api.defaults.adapter
let client: QueryClient | undefined

function adapterFactory(): AxiosAdapter {
  return async (config) => {
    if (config.method === 'get') {
      let data: unknown = { success: true, data: [], vendors: [] }
      if (config.url === '/api/group/') {
        data = { success: true, data: ['酒馆'] }
      } else if (config.url === '/api/status') {
        data = { success: true, data: {} }
      }
      return { data, status: 200, statusText: 'OK', headers: {}, config }
    }
    throw new Error(`unexpected ${config.method} ${config.url}`)
  }
}

afterEach(() => {
  client?.clear()
  api.defaults.adapter = originalAdapter
  localStorage.clear()
  vi.restoreAllMocks()
})

const FIXED_PRICE_ENTRIES = {
  酒馆: {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("request", fixed(0.015))',
  },
}

function renderEditor(handles: Map<string, ModelPricingEditorPanelHandle>) {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  usePricingPreferencesStore.setState({ currency: 'USD' })
  api.defaults.adapter = adapterFactory()
  const props = {
    modelName: 'claude-opus-4-6',
    entries: FIXED_PRICE_ENTRIES,
    activeGroups: ['酒馆'],
    onActiveGroupsChange: () => {},
    registerEditor: (
      group: string,
      handle: ModelPricingEditorPanelHandle | null
    ) => {
      if (handle) handles.set(group, handle)
      else handles.delete(group)
    },
  }
  return render(
    <QueryClientProvider client={client}>
      <GroupPricingOverridesEditor {...props} />
    </QueryClientProvider>
  )
}

it('keeps an in-progress group price edit across parent re-renders and commits it', async () => {
  const handles = new Map<string, ModelPricingEditorPanelHandle>()
  const view = renderEditor(handles)

  const priceInput = await screen.findByLabelText('Price per request')
  expect(priceInput).toHaveValue('0.015')

  // 用户把按次价从 0.015 改为 0.0125
  fireEvent.change(priceInput, { target: { value: '0.0125' } })
  expect(priceInput).toHaveValue('0.0125')

  // 编辑触发 dirty 上报，父组件（乃至整个页面）因此重渲染——
  // 与生产 props 一样，entries 引用保持稳定
  assert(client)
  view.rerender(
    <QueryClientProvider client={client}>
      <GroupPricingOverridesEditor
        modelName='claude-opus-4-6'
        entries={FIXED_PRICE_ENTRIES}
        activeGroups={['酒馆']}
        onActiveGroupsChange={() => {}}
        registerEditor={(group, handle) => {
          if (handle) handles.set(group, handle)
          else handles.delete(group)
        }}
      />
    </QueryClientProvider>
  )

  // 重渲染后编辑必须保留，且提交时产出新价格
  expect(screen.getByLabelText('Price per request')).toHaveValue('0.0125')
  const handle = handles.get('酒馆')
  assert(handle)
  const draft = await handle.commitDraft()
  expect(draft?.billingExpr).toBe('tier("request", fixed(0.0125))')
})
