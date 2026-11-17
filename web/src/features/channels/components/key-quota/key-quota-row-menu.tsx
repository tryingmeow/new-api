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
import { Eraser, MoreHorizontal, Pencil, Play } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { handleServerError } from '@/lib/handle-server-error'
import { createServerError, getServerMessage } from '@/lib/server-error-message'

import { resetChannelKeyUsage, resumeChannelKey } from '../../api'
import { MULTI_KEY_STATUS } from '../../constants'
import type { ChannelKeyRow, KeyQuotaConfig } from '../../types'
import { EditKeyDialog } from './edit-key-dialog'

type KeyQuotaRowMenuProps = {
  channelId: number
  isMultiKey: boolean
  row: ChannelKeyRow
  channelQuota: KeyQuotaConfig | null
  /** Called after a change so the caller reloads its rows. */
  onChanged: () => void
}

type ConfirmKind = 'reset' | 'resume'

/** Edit, clear this month's usage and resume now, for one key. */
export function KeyQuotaRowMenu(props: KeyQuotaRowMenuProps) {
  const { t } = useTranslation()
  const [editOpen, setEditOpen] = useState(false)
  const [confirm, setConfirm] = useState<ConfirmKind | null>(null)
  const paused = props.row.status === MULTI_KEY_STATUS.QUOTA_PAUSED

  const mutation = useMutation({
    mutationFn: async (kind: ConfirmKind) => {
      const res =
        kind === 'reset'
          ? await resetChannelKeyUsage(props.channelId, props.row.fingerprint)
          : await resumeChannelKey(props.channelId, props.row.fingerprint)
      if (!res.success) throw createServerError(res, t('Operation failed'))
      return res
    },
    onSuccess: (res) => {
      toast.success(getServerMessage(res) || t('Operation successful'))
      props.onChanged()
    },
    onError: (error) => handleServerError(error, t('Operation failed')),
    onSettled: () => setConfirm(null),
  })

  let confirmTitle = ''
  let confirmDesc = ''
  if (confirm === 'reset') {
    confirmTitle = t("Clear this month's usage?")
    confirmDesc = t(
      'The recorded usage of this key for this month goes back to 0 and a paused key starts working again. The upstream still counts what was really used, so going on may make the upstream charge your card.'
    )
  } else if (confirm === 'resume') {
    confirmTitle = t('Resume this key now?')
    confirmDesc = t(
      'The key starts working again before the refresh day. If it is still over the stop line it pauses again on its next request. Going on may make the upstream charge your card.'
    )
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              className='data-popup-open:bg-muted flex h-8 w-8 p-0'
            />
          }
        >
          <MoreHorizontal className='h-4 w-4' />
          <span className='sr-only'>{t('Open menu')}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-44'>
          <DropdownMenuItem onClick={() => setEditOpen(true)}>
            {t('Edit')}
            <DropdownMenuShortcut>
              <Pencil size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => setConfirm('reset')}>
            {t("Clear this month's usage")}
            <DropdownMenuShortcut>
              <Eraser size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          {paused && (
            <DropdownMenuItem onClick={() => setConfirm('resume')}>
              {t('Resume now')}
              <DropdownMenuShortcut>
                <Play size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      {editOpen && (
        <EditKeyDialog
          open
          onOpenChange={setEditOpen}
          channelId={props.channelId}
          isMultiKey={props.isMultiKey}
          row={props.row}
          channelQuota={props.channelQuota}
          onSaved={props.onChanged}
        />
      )}

      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={confirmTitle}
        desc={confirmDesc}
        destructive
        confirmText={confirm === 'resume' ? t('Resume now') : t('Clear')}
        isLoading={mutation.isPending}
        handleConfirm={() => confirm && mutation.mutate(confirm)}
      />
    </>
  )
}
