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
import { Loader2, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { translateServerText } from '@/lib/server-error-message'

import { useChannelKeys } from '../../hooks/use-channel-keys'
import { getMultiKeyStatusConfig } from '../../lib/multi-key-utils'
import type { ChannelKeyRow } from '../../types'
import { KeyNameCell, KeyRestoreCell, KeyUsageCell } from './key-quota-cells'
import { KeyQuotaRowMenu } from './key-quota-row-menu'

type KeyUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelId: number
  channelName: string
}

/** Usage and names of a channel's keys; the only key view of a single-key channel. */
export function KeyUsageDialog(props: KeyUsageDialogProps) {
  const { t } = useTranslation()
  const keysQuery = useChannelKeys(props.channelId, props.open)
  const data = keysQuery.data
  const keys = data?.keys ?? []

  let body: React.ReactNode
  if (keysQuery.isLoading) {
    body = (
      <div className='flex items-center justify-center py-12'>
        <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
      </div>
    )
  } else if (keys.length === 0) {
    body = (
      <div className='text-muted-foreground py-12 text-center'>
        {t('No keys found')}
      </div>
    )
  } else {
    body = (
      <StaticDataTable<ChannelKeyRow>
        className='rounded-none border-0'
        tableClassName='min-w-[680px]'
        data={keys}
        getRowKey={(row) => row.fingerprint || row.index}
        columns={[
          {
            id: 'name',
            header: t('Key'),
            cell: (row) => <KeyNameCell row={row} />,
          },
          {
            id: 'status',
            header: t('Status'),
            className: 'w-32',
            cell: (row) => {
              const config = getMultiKeyStatusConfig(row.status)
              return (
                <StatusBadge
                  label={t(config.label)}
                  variant={config.variant}
                  copyable={false}
                  title={
                    row.disabled_reason
                      ? translateServerText(t, row.disabled_reason)
                      : undefined
                  }
                />
              )
            },
          },
          {
            id: 'usage',
            header: t('Current month'),
            className: 'w-60',
            cell: (row) => <KeyUsageCell row={row} />,
          },
          {
            id: 'restore',
            header: t('Resumes at'),
            className: 'w-40',
            cell: (row) => <KeyRestoreCell row={row} />,
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'w-16 text-right',
            cellClassName: 'text-right',
            cell: (row) => (
              <KeyQuotaRowMenu
                channelId={props.channelId}
                isMultiKey={data?.is_multi_key === true}
                row={row}
                channelQuota={data?.quota ?? null}
                onChanged={keysQuery.refresh}
              />
            ),
          },
        ]}
      />
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <>
          {t('Key usage')}
          {props.channelName && (
            <StatusBadge
              label={props.channelName}
              variant='neutral'
              copyable={false}
            />
          )}
        </>
      }
      titleClassName='flex items-center gap-2'
      description={t(
        "This month's usage is counted by this site; the upstream bill may differ slightly"
      )}
      contentClassName='max-h-[min(90dvh,var(--dialog-available-height))] max-w-4xl sm:max-w-3xl'
      contentHeight='auto'
      bodyClassName='space-y-3'
    >
      <div className='flex justify-end'>
        <Button
          variant='outline'
          size='sm'
          onClick={keysQuery.refresh}
          disabled={keysQuery.isFetching}
          aria-label={t('Refresh')}
        >
          <RefreshCw className='h-4 w-4' />
        </Button>
      </div>
      <div className='overflow-auto rounded-md border'>{body}</div>
    </Dialog>
  )
}
