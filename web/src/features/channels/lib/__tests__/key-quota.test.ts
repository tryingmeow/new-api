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
import { describe, expect, test } from 'vitest'

import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  type ChannelFormValues,
} from '../channel-form'
import {
  computeNextReset,
  DEFAULT_KEY_QUOTA,
  formatResetHour,
  getKeyQuotaLevel,
  validateKeyQuota,
} from '../key-quota'

const ENABLED_QUOTA = {
  ...DEFAULT_KEY_QUOTA,
  enabled: true,
  monthly_quota: 5_000_000,
  reset_day: 5,
  reset_hour: 8,
}

function form(overrides: Partial<ChannelFormValues> = {}): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'OpenAI',
    type: 1,
    key: 'sk-test',
    models: 'gpt-4o',
    ...overrides,
  }
}

function savedSettings(values: ChannelFormValues): Record<string, unknown> {
  const payload = transformFormDataToCreatePayload(values)
  return JSON.parse(payload.channel.settings ?? '{}')
}

describe('computeNextReset', () => {
  test('returns this month when the refresh time has not passed', () => {
    const next = computeNextReset(5, 8, 0, new Date(Date.UTC(2026, 10, 3, 12)))
    expect(formatResetHour(next, 0)).toBe('2026-11-05 08:00')
  })

  test('returns next month when the refresh time has passed', () => {
    const next = computeNextReset(5, 8, 0, new Date(Date.UTC(2026, 10, 5, 8)))
    expect(formatResetHour(next, 0)).toBe('2026-12-05 08:00')
  })

  test('uses the last day of a month shorter than the refresh day', () => {
    const next = computeNextReset(31, 0, 0, new Date(Date.UTC(2027, 1, 10)))
    expect(formatResetHour(next, 0)).toBe('2027-02-28 00:00')
  })

  test('uses the server time zone, not the browser one', () => {
    const offset = 8 * 3600
    // 2026-11-30 17:00 UTC is already 2026-12-01 01:00 on a UTC+8 server.
    const now = new Date(Date.UTC(2026, 10, 30, 17))
    const next = computeNextReset(1, 0, offset, now)
    expect(formatResetHour(next, offset)).toBe('2027-01-01 00:00')
    expect(next.getTime()).toBe(Date.UTC(2026, 11, 31, 16))
  })
})

describe('validateKeyQuota', () => {
  test('accepts a disabled cap without an amount', () => {
    expect(validateKeyQuota(DEFAULT_KEY_QUOTA)).toEqual([])
  })

  test('rejects an enabled cap without an amount', () => {
    const issues = validateKeyQuota({ ...ENABLED_QUOTA, monthly_quota: 0 })
    expect(issues.map((issue) => issue.field)).toEqual(['monthly_quota'])
  })

  test('rejects a warning line at or above the stop line', () => {
    const issues = validateKeyQuota({
      ...ENABLED_QUOTA,
      warn_percent: 90,
      stop_percent: 90,
    })
    expect(issues.map((issue) => issue.field)).toEqual(['warn_percent'])
  })
})

describe('getKeyQuotaLevel', () => {
  test('maps usage to the warn and stop lines', () => {
    expect(getKeyQuotaLevel(79.9, 80, 90)).toBe('normal')
    expect(getKeyQuotaLevel(80, 80, 90)).toBe('warn')
    expect(getKeyQuotaLevel(90, 80, 90)).toBe('stop')
    expect(getKeyQuotaLevel(null, 80, 90)).toBe('normal')
  })
})

describe('key_quota in channel settings', () => {
  test('saves an enabled cap into the settings JSON', () => {
    expect(savedSettings(form({ key_quota: ENABLED_QUOTA })).key_quota).toEqual(
      ENABLED_QUOTA
    )
  })

  test('leaves out a cap that was never turned on', () => {
    expect(savedSettings(form())).not.toHaveProperty('key_quota')
  })

  test('keeps a turned-off cap that was saved before', () => {
    const settings = savedSettings(
      form({
        settings: JSON.stringify({ key_quota: ENABLED_QUOTA }),
        key_quota: { ...ENABLED_QUOTA, enabled: false },
      })
    )
    expect(settings.key_quota).toMatchObject({ enabled: false, reset_day: 5 })
  })

  test('loads the saved cap when editing a channel', () => {
    const channel = channelSchema.parse({
      id: 7,
      name: 'OpenAI',
      type: 1,
      key: '',
      status: 4,
      created_time: 1,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      models: 'gpt-4o',
      group: 'default',
      settings: JSON.stringify({ key_quota: ENABLED_QUOTA }),
    })
    expect(transformChannelToFormDefaults(channel).key_quota).toEqual(
      ENABLED_QUOTA
    )
  })

  test('reports an invalid enabled cap on its field', () => {
    const result = channelFormSchema.safeParse(
      form({ key_quota: { ...ENABLED_QUOTA, reset_day: 32 } })
    )
    expect(result.success).toBe(false)
    expect(result.error?.issues.map((issue) => issue.path.join('.'))).toEqual([
      'key_quota.reset_day',
    ])
  })
})
