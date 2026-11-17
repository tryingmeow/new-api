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
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { getCurrencyLabel } from '@/lib/currency'
import {
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToEditableAmount,
} from '@/lib/format'

import { useServerUtcOffset } from '../../hooks/use-server-utc-offset'
import {
  computeNextReset,
  formatResetHour,
  type KeyQuotaField,
} from '../../lib/key-quota'
import type { KeyQuotaConfig } from '../../types'

type KeyQuotaFieldsProps = {
  value: KeyQuotaConfig
  onChange: (value: KeyQuotaConfig) => void
  /** i18n keys of the fields' errors. */
  errors?: Partial<Record<KeyQuotaField, string | undefined>>
  disabled?: boolean
}

function MonthlyQuotaInput(props: {
  id: string
  value: number
  onChange: (quota: number) => void
  disabled?: boolean
  invalid: boolean
  describedBy?: string
}) {
  const toText = (quota: number) =>
    quota > 0 ? String(quotaUnitsToEditableAmount(quota)) : ''
  const [text, setText] = useState(() => toText(props.value))
  const [editing, setEditing] = useState(false)

  useEffect(() => {
    if (!editing) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setText(toText(props.value))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.value, editing])

  return (
    <Input
      id={props.id}
      type='number'
      inputMode='decimal'
      min={0}
      step={getEditableQuotaStep()}
      value={text}
      disabled={props.disabled}
      aria-invalid={props.invalid || undefined}
      aria-describedby={props.describedBy}
      onFocus={() => setEditing(true)}
      onBlur={() => setEditing(false)}
      onChange={(e) => {
        setText(e.target.value)
        const amount = Number.parseFloat(e.target.value)
        props.onChange(
          Number.isFinite(amount) ? parseQuotaFromDollars(amount) : 0
        )
      }}
    />
  )
}

function IntegerInput(props: {
  id: string
  value: number
  min: number
  max: number
  onChange: (value: number) => void
  disabled?: boolean
  invalid: boolean
  describedBy?: string
}) {
  return (
    <Input
      id={props.id}
      type='number'
      inputMode='numeric'
      min={props.min}
      max={props.max}
      step={1}
      value={props.value}
      disabled={props.disabled}
      aria-invalid={props.invalid || undefined}
      aria-describedby={props.describedBy}
      onChange={(e) => {
        const parsed = Number.parseInt(e.target.value, 10)
        props.onChange(Number.isFinite(parsed) ? parsed : 0)
      }}
    />
  )
}

/**
 * Monthly quota cap: amount, refresh time and the warn/stop lines. Shared by
 * the channel drawer and the key editor.
 */
export function KeyQuotaFields(props: KeyQuotaFieldsProps) {
  const { t } = useTranslation()
  const baseId = useId()
  const value = props.value
  const set = <K extends KeyQuotaField>(field: K, next: KeyQuotaConfig[K]) =>
    props.onChange({ ...value, [field]: next })
  const error = (field: KeyQuotaField) => props.errors?.[field]
  const errorId = (field: KeyQuotaField) =>
    error(field) ? `${baseId}-${field}-error` : undefined
  const utcOffset = useServerUtcOffset()
  const fieldsDisabled = props.disabled || !value.enabled
  const validTime =
    value.reset_day >= 1 &&
    value.reset_day <= 31 &&
    value.reset_hour >= 0 &&
    value.reset_hour <= 23
  const nextReset = validTime
    ? formatResetHour(
        computeNextReset(value.reset_day, value.reset_hour, utcOffset),
        utcOffset
      )
    : '-'
  const currency = getCurrencyLabel()

  const renderError = (field: KeyQuotaField) => {
    const message = error(field)
    if (!message) return null
    return <FieldError id={errorId(field)}>{t(message)}</FieldError>
  }

  return (
    <div className='space-y-4'>
      <Field orientation='horizontal' className='justify-between'>
        <div className='space-y-0.5'>
          <FieldLabel htmlFor={`${baseId}-enabled`}>
            {t('Enable quota cap')}
          </FieldLabel>
          <FieldDescription>
            {t(
              'Pause the key before it goes over its monthly credit, and resume it on the refresh day'
            )}
          </FieldDescription>
        </div>
        <Switch
          id={`${baseId}-enabled`}
          checked={value.enabled}
          disabled={props.disabled}
          onCheckedChange={(checked) => set('enabled', checked)}
        />
      </Field>

      <div className='grid gap-4 sm:grid-cols-2'>
        <Field className='sm:col-span-2'>
          <FieldLabel htmlFor={`${baseId}-monthly_quota`}>
            {t('Monthly quota ({{currency}})', { currency })}
          </FieldLabel>
          <MonthlyQuotaInput
            id={`${baseId}-monthly_quota`}
            value={value.monthly_quota}
            disabled={fieldsDisabled}
            invalid={Boolean(error('monthly_quota'))}
            describedBy={errorId('monthly_quota')}
            onChange={(quota) => set('monthly_quota', quota)}
          />
          {renderError('monthly_quota')}
        </Field>

        <Field>
          <FieldLabel htmlFor={`${baseId}-reset_day`}>
            {t('Refresh day (1-31)')}
          </FieldLabel>
          <IntegerInput
            id={`${baseId}-reset_day`}
            value={value.reset_day}
            min={1}
            max={31}
            disabled={fieldsDisabled}
            invalid={Boolean(error('reset_day'))}
            describedBy={errorId('reset_day')}
            onChange={(day) => set('reset_day', day)}
          />
          {renderError('reset_day')}
        </Field>

        <Field>
          <FieldLabel htmlFor={`${baseId}-reset_hour`}>
            {t('Refresh hour (0-23)')}
          </FieldLabel>
          <IntegerInput
            id={`${baseId}-reset_hour`}
            value={value.reset_hour}
            min={0}
            max={23}
            disabled={fieldsDisabled}
            invalid={Boolean(error('reset_hour'))}
            describedBy={errorId('reset_hour')}
            onChange={(hour) => set('reset_hour', hour)}
          />
          {renderError('reset_hour')}
        </Field>

        <p className='text-muted-foreground text-xs sm:col-span-2'>
          {t('Next refresh: {{time}}', { time: nextReset })}
          {' · '}
          {t('A shorter month uses its last day; times are server time')}
        </p>

        <Field>
          <FieldLabel htmlFor={`${baseId}-stop_percent`}>
            {t('Stop line (%)')}
          </FieldLabel>
          <IntegerInput
            id={`${baseId}-stop_percent`}
            value={value.stop_percent}
            min={1}
            max={100}
            disabled={fieldsDisabled}
            invalid={Boolean(error('stop_percent'))}
            describedBy={errorId('stop_percent')}
            onChange={(percent) => set('stop_percent', percent)}
          />
          {renderError('stop_percent')}
        </Field>

        <Field>
          <FieldLabel htmlFor={`${baseId}-warn_percent`}>
            {t('Warning line (%)')}
          </FieldLabel>
          <IntegerInput
            id={`${baseId}-warn_percent`}
            value={value.warn_percent}
            min={1}
            max={99}
            disabled={fieldsDisabled}
            invalid={Boolean(error('warn_percent'))}
            describedBy={errorId('warn_percent')}
            onChange={(percent) => set('warn_percent', percent)}
          />
          {renderError('warn_percent')}
        </Field>
      </div>
    </div>
  )
}
