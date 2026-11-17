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
import { useMutation } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { handleServerError } from '@/lib/handle-server-error'
import { createServerError } from '@/lib/server-error-message'

import { saveChannelKeyMeta } from '../../api'
import {
  DEFAULT_KEY_QUOTA,
  normalizeKeyQuota,
  validateKeyQuota,
  type KeyQuotaField,
} from '../../lib/key-quota'
import type { ChannelKeyRow, KeyQuotaConfig } from '../../types'
import { KeyQuotaFields } from './key-quota-fields'

type EditKeyDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelId: number
  isMultiKey: boolean
  row: ChannelKeyRow
  /** The channel's default cap, the starting point of a custom cap. */
  channelQuota: KeyQuotaConfig | null
  onSaved: () => void
}

type QuotaMode = 'inherit' | 'custom'

function initialQuota(
  row: ChannelKeyRow,
  channelQuota: KeyQuotaConfig | null
): KeyQuotaConfig {
  if (row.quota_mode === 'custom' && row.effective_quota) {
    return normalizeKeyQuota(row.effective_quota)
  }
  // A custom cap that is turned off comes back without its config.
  const enabled = row.quota_mode !== 'custom'
  return { ...(channelQuota ?? DEFAULT_KEY_QUOTA), enabled }
}

export function EditKeyDialog(props: EditKeyDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState(props.row.name ?? '')
  const [mode, setMode] = useState<QuotaMode>(
    props.row.quota_mode === 'custom' ? 'custom' : 'inherit'
  )
  const [quota, setQuota] = useState<KeyQuotaConfig>(() =>
    initialQuota(props.row, props.channelQuota)
  )
  const [showErrors, setShowErrors] = useState(false)

  const issues = mode === 'custom' ? validateKeyQuota(quota) : []
  const errors: Partial<Record<KeyQuotaField, string>> = {}
  if (showErrors) {
    for (const issue of issues) errors[issue.field] = issue.message
  }

  const mutation = useMutation({
    mutationFn: async () => {
      // A single-key channel keeps its cap on the channel.
      const quotaMode: QuotaMode = props.isMultiKey ? mode : 'inherit'
      const res = await saveChannelKeyMeta(props.channelId, {
        fingerprint: props.row.fingerprint,
        name: name.trim(),
        quota_mode: quotaMode,
        quota: quotaMode === 'custom' ? quota : null,
      })
      if (!res.success) throw createServerError(res, t('Failed to save key'))
      return res
    },
    onSuccess: () => {
      toast.success(t('Key saved'))
      props.onSaved()
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error, t('Failed to save key')),
  })

  const handleSave = () => {
    if (props.isMultiKey && issues.length > 0) {
      setShowErrors(true)
      return
    }
    mutation.mutate()
  }

  const modeOptions: { value: QuotaMode; label: string }[] = [
    { value: 'inherit', label: t('Follow channel default') },
    { value: 'custom', label: t('Set separately') },
  ]

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Edit key')}
      description={props.row.preview}
      descriptionClassName='font-mono'
      contentClassName='max-h-[min(90dvh,var(--dialog-available-height))] max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-5'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleSave} disabled={mutation.isPending}>
            {mutation.isPending && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            )}
            {t('Save Changes')}
          </Button>
        </>
      }
    >
      <div className='space-y-2'>
        <Label htmlFor='edit-key-name'>{t('Key name')}</Label>
        <Input
          id='edit-key-name'
          value={name}
          maxLength={64}
          placeholder={t('Optional, e.g. card ending 1234')}
          onChange={(e) => setName(e.target.value)}
        />
      </div>

      {props.isMultiKey ? (
        <div className='space-y-3'>
          <Label id='edit-key-quota-mode'>{t('Quota cap')}</Label>
          <RadioGroup
            aria-labelledby='edit-key-quota-mode'
            value={mode}
            onValueChange={(value) => setMode(value as QuotaMode)}
            className='grid gap-2 sm:grid-cols-2'
          >
            {modeOptions.map((option) => (
              <Label
                key={option.value}
                htmlFor={`edit-key-mode-${option.value}`}
                className='hover:bg-muted/30 flex cursor-pointer items-center gap-3 rounded-lg border px-4 py-3 font-normal'
              >
                <RadioGroupItem
                  id={`edit-key-mode-${option.value}`}
                  value={option.value}
                />
                {option.label}
              </Label>
            ))}
          </RadioGroup>
          {mode === 'custom' ? (
            <div className='rounded-lg border p-4'>
              <KeyQuotaFields
                value={quota}
                onChange={setQuota}
                errors={errors}
                disabled={mutation.isPending}
              />
            </div>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {props.channelQuota?.enabled
                ? t('Uses the cap set on the channel')
                : t('The channel has no cap, so this key has none either')}
            </p>
          )}
        </div>
      ) : (
        <p className='text-muted-foreground text-xs'>
          {t('The cap of a single-key channel is set in the channel settings')}
        </p>
      )}
    </Dialog>
  )
}
