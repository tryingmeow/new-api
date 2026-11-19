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
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber } from '@/lib/format'

import { MULTI_KEY_STATUS } from '../../constants'
import { useServerUtcOffset } from '../../hooks/use-server-utc-offset'
import { formatResetHour } from '../../lib/key-quota'
import type { ChannelKeyRow } from '../../types'
import { KeyQuotaUsage } from './key-quota-usage'

/** Name, masked key and a tag when the key has its own cap. */
export function KeyNameCell(props: { row: ChannelKeyRow }) {
  const { t } = useTranslation()
  return (
    <div className='flex min-w-0 flex-col gap-0.5'>
      <div className='flex items-center gap-1.5'>
        <span
          className={
            props.row.name ? 'truncate font-medium' : 'text-muted-foreground'
          }
        >
          {props.row.name || t('Unnamed')}
        </span>
        {props.row.quota_mode === 'custom' && (
          <StatusBadge
            label={t('Own quota')}
            variant='info'
            size='sm'
            showDot={false}
            copyable={false}
          />
        )}
      </div>
      <span className='text-muted-foreground font-mono text-xs'>
        {props.row.preview || '-'}
      </span>
    </div>
  )
}

/** This period's usage, with a bar when the key has a cap. */
export function KeyUsageCell(props: { row: ChannelKeyRow }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const quota = props.row.effective_quota
  const requests = t('Requests: {{count}}', {
    count: formatNumber(props.row.request_count ?? 0, locale),
  })

  if (!quota?.enabled || !(quota.monthly_quota > 0)) {
    return (
      <div className='text-xs'>
        <div>
          {t('This month {{used}}', {
            used: formatQuotaWithCurrency(props.row.used_quota ?? 0, {
              digitsLarge: 2,
              digitsSmall: 2,
              abbreviate: false,
              locale,
            }),
          })}
        </div>
        <div className='text-muted-foreground'>{requests}</div>
      </div>
    )
  }

  return (
    <div className='max-w-60 space-y-0.5'>
      <KeyQuotaUsage
        usedQuota={props.row.used_quota ?? 0}
        limitQuota={quota.monthly_quota}
        percent={props.row.percent}
        warnPercent={quota.warn_percent}
        stopPercent={quota.stop_percent}
      />
      <div className='text-muted-foreground text-xs'>{requests}</div>
    </div>
  )
}

/** When a quota-paused key resumes. */
export function KeyRestoreCell(props: { row: ChannelKeyRow }) {
  const utcOffset = useServerUtcOffset()
  if (
    props.row.status !== MULTI_KEY_STATUS.QUOTA_PAUSED ||
    !props.row.next_reset_at
  ) {
    return <span className='text-muted-foreground'>-</span>
  }
  return (
    <span className='text-sm whitespace-nowrap'>
      {formatResetHour(new Date(props.row.next_reset_at * 1000), utcOffset)}
    </span>
  )
}
