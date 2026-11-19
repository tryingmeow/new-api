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

import { Progress } from '@/components/ui/progress'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { cn } from '@/lib/utils'

import {
  getKeyQuotaLevel,
  KEY_QUOTA_LEVEL_INDICATOR_CLASS,
} from '../../lib/key-quota'

type KeyQuotaUsageProps = {
  usedQuota: number
  /** Quota units; 0 or less means no cap. */
  limitQuota: number
  percent?: number | null
  warnPercent: number
  stopPercent: number
  /** Hide the amounts (sensitive values hidden in the list). */
  masked?: boolean
  className?: string
}

function usagePercent(props: KeyQuotaUsageProps): number | null {
  if (props.percent != null && Number.isFinite(props.percent)) {
    return props.percent
  }
  if (props.limitQuota > 0) return (props.usedQuota / props.limitQuota) * 100
  return null
}

/** "This month $8.40 / $10" with a bar coloured by the warn and stop lines. */
export function KeyQuotaUsage(props: KeyQuotaUsageProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const format = (quota: number) =>
    props.masked
      ? '••••'
      : formatQuotaWithCurrency(quota, {
          digitsLarge: 2,
          digitsSmall: 2,
          abbreviate: false,
          locale,
        })
  const percent = usagePercent(props)
  const used = format(props.usedQuota)

  if (percent === null) {
    return (
      <div className={cn('text-xs', props.className)}>
        {t('This month {{used}}', { used })}
      </div>
    )
  }

  const level = getKeyQuotaLevel(percent, props.warnPercent, props.stopPercent)
  const label = t('This month {{used}} / {{limit}}', {
    used,
    limit: format(props.limitQuota),
  })

  return (
    <div className={cn('min-w-32 space-y-1', props.className)}>
      <div className='flex items-baseline justify-between gap-2 text-xs'>
        <span className='whitespace-nowrap'>{label}</span>
        <span className='text-muted-foreground tabular-nums'>
          {Math.round(percent)}%
        </span>
      </div>
      <Progress
        value={Math.min(100, Math.max(0, percent))}
        aria-label={label}
        className={cn('gap-0', KEY_QUOTA_LEVEL_INDICATOR_CLASS[level])}
      />
    </div>
  )
}
