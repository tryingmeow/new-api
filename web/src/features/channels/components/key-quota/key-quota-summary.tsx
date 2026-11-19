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

import { useServerUtcOffset } from '../../hooks/use-server-utc-offset'
import {
  DEFAULT_KEY_QUOTA,
  parseChannelKeyQuota,
  resetDayOfMonth,
} from '../../lib/key-quota'
import type { Channel } from '../../types'
import { KeyQuotaUsage } from './key-quota-usage'

/** The channel list's usage bar for this period, from key_quota_summary. */
export function KeyQuotaSummaryBar(props: {
  channel: Channel
  masked?: boolean
}) {
  const { t } = useTranslation()
  const summary = props.channel.key_quota_summary
  if (!summary || (!summary.enabled && !(summary.limit_quota > 0))) {
    return null
  }
  const config =
    parseChannelKeyQuota(props.channel.settings) ?? DEFAULT_KEY_QUOTA
  const isMultiKey = props.channel.channel_info?.is_multi_key === true
  const available = Math.max(0, summary.total_keys - summary.paused_keys)

  return (
    <div className='w-full max-w-56 space-y-0.5'>
      <KeyQuotaUsage
        usedQuota={summary.used_quota}
        limitQuota={summary.limit_quota}
        percent={summary.percent}
        warnPercent={summary.warn_percent || config.warn_percent}
        stopPercent={summary.stop_percent || config.stop_percent}
        masked={props.masked}
      />
      {isMultiKey && summary.total_keys > 0 && (
        <div className='text-muted-foreground text-xs'>
          {t('{{available}} available · {{paused}} paused', {
            available,
            paused: summary.paused_keys,
          })}
        </div>
      )}
    </div>
  )
}

/** "Resumes on day 5" under a quota-paused channel's status. */
export function QuotaResumeHint(props: {
  nextResetAt: number | null | undefined
}) {
  const { t } = useTranslation()
  const utcOffset = useServerUtcOffset()
  const day = resetDayOfMonth(props.nextResetAt, utcOffset)
  if (day === null) return null
  return (
    <span className='text-warning text-xs whitespace-nowrap'>
      {t('Resumes on day {{day}}', { day })}
    </span>
  )
}
