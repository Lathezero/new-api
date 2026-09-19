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
import { useQuery } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { GroupBadge } from '@/components/group-badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { pricingRow, type PricingValues } from '@/features/model-pricing/pricing'
import type { BillingUsageSchema } from '@/features/pricing/types'
import { getGroups } from '@/features/users/api'

import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
} from './model-pricing-sheet'

export type GroupPricingOverridesEditorProps = {
  modelName: string
  /** Configured override values per group, from the pricing snapshot entry. */
  entries: Record<string, PricingValues>
  activeGroups: string[]
  onActiveGroupsChange: (groups: string[]) => void
  registerEditor: (
    group: string,
    handle: ModelPricingEditorPanelHandle | null
  ) => void
  usageSchema?: BillingUsageSchema
  onDirtyChange?: (dirty: boolean) => void
}

export function GroupPricingOverridesEditor(
  props: GroupPricingOverridesEditorProps
) {
  const { t } = useTranslation()
  const [groupToAdd, setGroupToAdd] = useState('')
  const [groupToRemove, setGroupToRemove] = useState<string | null>(null)
  const groupDirty = useRef(new Map<string, boolean>())

  // Secondary data: a failure only limits adding new overrides, so degrade to
  // the already-configured groups without a global error toast.
  const groupsQuery = useQuery({
    queryKey: ['admin-groups'],
    queryFn: async () => {
      const response = await getGroups()
      return response.success && Array.isArray(response.data)
        ? response.data
        : []
    },
    retry: false,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })

  const candidateGroups = useMemo(() => {
    const all = new Set(groupsQuery.data ?? [])
    for (const group of props.activeGroups) all.add(group)
    return [...all].filter((group) => !props.activeGroups.includes(group))
  }, [groupsQuery.data, props.activeGroups])

  const reportDirty = (group: string, dirty: boolean) => {
    groupDirty.current.set(group, dirty)
    props.onDirtyChange?.([...groupDirty.current.values()].some(Boolean))
  }

  return (
    <section
      aria-label={t('Group pricing')}
      className='space-y-3 rounded-xl border p-4'
    >
      <div className='space-y-1'>
        <h3 className='text-sm font-medium'>{t('Group pricing')}</h3>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Override this model’s pricing for specific groups. Groups without an override use the pricing above. The group ratio still applies on top.'
          )}
        </p>
      </div>

      {props.activeGroups.map((group) => {
        const configured = props.entries[group]
        const editData =
          configured && Object.keys(configured).length > 0
            ? pricingRow(props.modelName, configured)
            : { name: props.modelName }
        return (
          <div key={group} className='overflow-hidden rounded-lg border'>
            <div className='bg-muted/20 flex items-center justify-between gap-3 border-b px-3 py-2'>
              <GroupBadge group={group} size='sm' />
              <Button
                type='button'
                variant='ghost'
                size='sm'
                aria-label={t('Remove group pricing override')}
                onClick={() => setGroupToRemove(group)}
              >
                <Trash2 data-icon='inline-start' />
                {t('Remove')}
              </Button>
            </div>
            <ModelPricingEditorPanel
              embedded
              ref={(handle) => props.registerEditor(group, handle)}
              editData={editData}
              usageSchema={props.usageSchema}
              onDirtyChange={(dirty) => reportDirty(group, dirty)}
              className='rounded-none border-0'
            />
          </div>
        )
      })}

      <div className='flex flex-wrap items-center gap-2'>
        <Select
          items={candidateGroups.map((group) => ({ value: group, label: group }))}
          value={groupToAdd}
          onValueChange={(value) => setGroupToAdd(value ?? '')}
        >
          <SelectTrigger
            aria-label={t('Select a group')}
            className='w-48'
            size='sm'
          >
            <SelectValue placeholder={t('Select a group')} />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            {candidateGroups.map((group) => (
              <SelectItem key={group} value={group}>
                {group}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={!groupToAdd}
          onClick={() => {
            if (!groupToAdd) return
            props.onActiveGroupsChange([...props.activeGroups, groupToAdd])
            setGroupToAdd('')
          }}
        >
          <Plus data-icon='inline-start' />
          {t('Add group pricing')}
        </Button>
      </div>

      <ConfirmDialog
        open={groupToRemove !== null}
        onOpenChange={(open) => {
          if (!open) setGroupToRemove(null)
        }}
        title={t('Remove group pricing override')}
        desc={t(
          'Remove the pricing override for group {{group}}? The group will use the model’s default pricing.',
          { group: groupToRemove ?? '' }
        )}
        confirmText={t('Remove')}
        destructive
        handleConfirm={() => {
          if (groupToRemove) {
            props.registerEditor(groupToRemove, null)
            groupDirty.current.delete(groupToRemove)
            props.onActiveGroupsChange(
              props.activeGroups.filter((group) => group !== groupToRemove)
            )
          }
          setGroupToRemove(null)
        }}
      />
    </section>
  )
}
