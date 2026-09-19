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
import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query'
import { t } from 'i18next'

import { pluginExpressionsEqual } from '@/features/pricing/lib/plugin-pricing'
import type {
  BillingUsageSchema,
  BillingUsageExample,
} from '@/features/pricing/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { createServerError } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  GROUP_MODEL_PRICING_KEY,
  PRICING_KEYS,
  groupPricingEqual,
  pricingValuesByModel,
  type GroupPricingValues,
  type PricingOptions,
  type PricingValues,
  type CacheWriteMode,
  type LegacyBillingDetails,
} from './pricing'

export type ModelPricingDescription = {
  billing_details?: LegacyBillingDetails
  effective: PricingValues
  cache_write_mode?: CacheWriteMode
}

export type ModelPricingPluginVariant = {
  plugin_key: string
  plugin_name: string
  icon?: string
  usage_schema: BillingUsageSchema
  usage_examples?: BillingUsageExample[]
  configured: string
  effective: string
  compatible: boolean
  stale?: boolean
}

export type ModelPricingGroupEntry = {
  configured: PricingValues
  effective: PricingValues
}

export type ModelPricingEntry = ModelPricingDescription & {
  plugin_variants?: ModelPricingPluginVariant[]
  model_name: string
  version: string
  configured: PricingValues
  groups?: Record<string, ModelPricingGroupEntry>
  usage_schema?: BillingUsageSchema
}

export type ModelPricingConfig = {
  entries: ModelPricingEntry[]
  options: PricingOptions
  empty_version: string
}
export type ModelPricingChange = {
  model_name: string
  expected_version: string
  pricing: PricingValues
  reset?: boolean
}

export type ModelPricingConversion = Partial<ModelPricingDescription> & {
  expression?: string
  unsupported_reason?: string
}

export async function previewModelPricingConversion(request: {
  model_name: string
  pricing: PricingValues
}): Promise<ModelPricingConversion> {
  const response = await api.post('/api/option/model_pricing/convert', request)
  if (!response.data.success) {
    throw createServerError(
      response.data,
      t('Failed to prepare pricing conversion')
    )
  }
  return response.data.data
}

export async function previewModelPricing(request: {
  model_name: string
  pricing: PricingValues
}): Promise<{
  effective: PricingValues
  cacheWriteMode?: CacheWriteMode
  billingDetails?: LegacyBillingDetails
}> {
  const response = await api.post('/api/option/model_pricing/preview', request)
  if (!response.data.success) {
    throw createServerError(response.data, t('Failed to load model pricing'))
  }
  return {
    effective: response.data.data.effective,
    cacheWriteMode: response.data.data.cache_write_mode,
    billingDetails: response.data.data.billing_details,
  }
}

export function useCanEditModelPricing() {
  return useAuthStore((state) => state.auth.user?.role === ROLE.SUPER_ADMIN)
}

export async function getModelPricing(
  names: string[] = []
): Promise<ModelPricingConfig> {
  const params = new URLSearchParams()
  for (const name of names) params.append('model', name)
  const res = await api.get('/api/option/model_pricing', { params })
  if (!res.data.success) {
    throw createServerError(res.data, t('Failed to load model pricing'))
  }
  return res.data.data
}

export function useModelPricing(names: string[] = [], enabled = true) {
  const canEdit = useCanEditModelPricing()
  return useQuery({
    queryKey: ['model-pricing-config', ...names],
    queryFn: () => getModelPricing(names),
    enabled: enabled && canEdit,
    refetchOnWindowFocus: false,
  })
}

export async function invalidateModelPricing(client: QueryClient) {
  await Promise.all([
    client.invalidateQueries({ queryKey: ['model-pricing-config'] }),
    client.invalidateQueries({ queryKey: ['model-pricing-preview'] }),
    client.invalidateQueries({ queryKey: ['system-options'] }),
    client.invalidateQueries({ queryKey: ['pricing'] }),
    client.invalidateQueries({ queryKey: ['models'] }),
  ])
}

export async function saveModelPricing(changes: ModelPricingChange[]) {
  if (!changes.length) return
  const res = await api.patch('/api/option/model_pricing', { changes })
  if (!res.data.success) {
    throw createServerError(res.data, t('Failed to save model pricing'))
  }
}

export function useSaveModelPricing() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: saveModelPricing,
    onSuccess: () => invalidateModelPricing(client),
  })
}

// groupPricingByModel reconstructs the (model → group → fields) override map
// from a pricing snapshot, for diffing against edited form state.
export function groupPricingByModel(
  snapshot: ModelPricingConfig
): Record<string, Record<string, GroupPricingValues>> {
  const result: Record<string, Record<string, GroupPricingValues>> = {}
  for (const entry of snapshot.entries) {
    const groups = entry.configured[GROUP_MODEL_PRICING_KEY]
    if (groups && Object.keys(groups).length > 0) {
      result[entry.model_name] = groups
    }
  }
  return result
}

// Only dirty model fields are applied to stored configuration. Display-only
// built-in expressions for other models never become administrator overrides.
// groupPricing carries the (model → group → fields) maps before and after the
// edit; a model with only group-override changes still produces a change.
export function buildPricingChanges(
  snapshot: ModelPricingConfig,
  before: PricingOptions,
  after: PricingOptions,
  groupPricing?: {
    before: Record<string, Record<string, GroupPricingValues>>
    after: Record<string, Record<string, GroupPricingValues>>
  }
): ModelPricingChange[] {
  const previous = pricingValuesByModel(before)
  const next = pricingValuesByModel(after)
  const groupBefore = groupPricing?.before ?? {}
  const groupAfter = groupPricing?.after ?? {}
  const entries = new Map(
    snapshot.entries.map((entry) => [entry.model_name, entry])
  )
  const changes: ModelPricingChange[] = []
  const names = new Set([
    ...previous.keys(),
    ...next.keys(),
    ...Object.keys(groupBefore),
    ...Object.keys(groupAfter),
  ])
  for (const name of names) {
    const oldValues = previous.get(name) ?? {}
    const newValues = next.get(name) ?? {}
    const dirty = PRICING_KEYS.filter((key) =>
      key === 'billing_setting.plugin_billing_expr'
        ? !pluginExpressionsEqual(oldValues[key], newValues[key])
        : oldValues[key] !== newValues[key]
    )
    const groupsChanged = !groupPricingEqual(groupBefore[name], groupAfter[name])
    if (!dirty.length && !groupsChanged) continue
    const entry = entries.get(name)
    const pricing = { ...entry?.configured }
    for (const key of dirty) {
      delete pricing[key]
      if (newValues[key] !== undefined) {
        Object.assign(pricing, { [key]: newValues[key] })
      }
    }
    if (groupsChanged) {
      delete pricing[GROUP_MODEL_PRICING_KEY]
      const groups = groupAfter[name]
      if (groups && Object.keys(groups).length > 0) {
        pricing[GROUP_MODEL_PRICING_KEY] = groups
      }
    }
    if (newValues['billing_setting.billing_mode'] === 'tiered_expr') {
      pricing['billing_setting.billing_mode'] = 'tiered_expr'
      pricing['billing_setting.billing_expr'] =
        newValues['billing_setting.billing_expr']
    }
    changes.push({
      model_name: name,
      expected_version: entry?.version ?? snapshot.empty_version,
      pricing,
    })
  }
  return changes
}
