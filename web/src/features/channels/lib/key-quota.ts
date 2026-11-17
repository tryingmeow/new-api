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
import type { KeyQuotaConfig } from '../types'

export const DEFAULT_KEY_QUOTA: KeyQuotaConfig = {
  enabled: false,
  monthly_quota: 0,
  reset_day: 1,
  reset_hour: 0,
  stop_percent: 90,
  warn_percent: 80,
}

export type KeyQuotaField = keyof KeyQuotaConfig

export type KeyQuotaIssue = {
  field: KeyQuotaField
  /** An i18n key. */
  message: string
}

export type KeyQuotaLevel = 'normal' | 'warn' | 'stop'

function toInt(value: unknown, fallback: number): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? Math.round(parsed) : fallback
}

/** Fill a stored or partial config with the defaults. */
export function normalizeKeyQuota(value: unknown): KeyQuotaConfig {
  if (!value || typeof value !== 'object') return { ...DEFAULT_KEY_QUOTA }
  const raw = value as Record<string, unknown>
  return {
    enabled: raw.enabled === true,
    monthly_quota: toInt(raw.monthly_quota, DEFAULT_KEY_QUOTA.monthly_quota),
    reset_day: toInt(raw.reset_day, DEFAULT_KEY_QUOTA.reset_day),
    reset_hour: toInt(raw.reset_hour, DEFAULT_KEY_QUOTA.reset_hour),
    stop_percent: toInt(raw.stop_percent, DEFAULT_KEY_QUOTA.stop_percent),
    warn_percent: toInt(raw.warn_percent, DEFAULT_KEY_QUOTA.warn_percent),
  }
}

/** The channel's `key_quota` from its settings JSON, or null when unset. */
export function parseChannelKeyQuota(
  settings: string | null | undefined
): KeyQuotaConfig | null {
  if (!settings) return null
  try {
    const parsed = JSON.parse(settings) as Record<string, unknown> | null
    if (!parsed || typeof parsed !== 'object' || !parsed.key_quota) {
      return null
    }
    return normalizeKeyQuota(parsed.key_quota)
  } catch {
    return null
  }
}

/** Mirrors the server validation; only an enabled config is checked. */
export function validateKeyQuota(config: KeyQuotaConfig): KeyQuotaIssue[] {
  if (!config.enabled) return []
  const issues: KeyQuotaIssue[] = []
  if (!(config.monthly_quota > 0)) {
    issues.push({
      field: 'monthly_quota',
      message: 'Monthly quota must be greater than 0',
    })
  }
  if (config.reset_day < 1 || config.reset_day > 31) {
    issues.push({
      field: 'reset_day',
      message: 'Refresh day must be between 1 and 31',
    })
  }
  if (config.reset_hour < 0 || config.reset_hour > 23) {
    issues.push({
      field: 'reset_hour',
      message: 'Refresh hour must be between 0 and 23',
    })
  }
  if (config.stop_percent < 1 || config.stop_percent > 100) {
    issues.push({
      field: 'stop_percent',
      message: 'Stop line must be between 1% and 100%',
    })
  }
  if (config.warn_percent < 1 || config.warn_percent >= config.stop_percent) {
    issues.push({
      field: 'warn_percent',
      message: 'Warning line must be at least 1% and below the stop line',
    })
  }
  return issues
}

/**
 * Quotas reset in server local time. `offsetSeconds` is the server's UTC
 * offset (`server_utc_offset` from /api/status); the returned Date's UTC
 * fields read as the server's wall clock.
 */
function serverWallClock(ms: number, offsetSeconds: number): Date {
  return new Date(ms + offsetSeconds * 1000)
}

function resetDateIn(
  year: number,
  month: number,
  day: number,
  hour: number,
  offsetSeconds: number
): Date {
  const lastDay = new Date(Date.UTC(year, month + 1, 0)).getUTCDate()
  return new Date(
    Date.UTC(year, month, Math.min(day, lastDay), hour) - offsetSeconds * 1000
  )
}

/**
 * The next refresh after `now`: reset_day at reset_hour in server local time,
 * using the last day of a month that is shorter than reset_day.
 */
export function computeNextReset(
  resetDay: number,
  resetHour: number,
  offsetSeconds: number,
  now: Date = new Date()
): Date {
  const wall = serverWallClock(now.getTime(), offsetSeconds)
  const year = wall.getUTCFullYear()
  const month = wall.getUTCMonth()
  const thisMonth = resetDateIn(year, month, resetDay, resetHour, offsetSeconds)
  if (thisMonth.getTime() > now.getTime()) return thisMonth
  return resetDateIn(year, month + 1, resetDay, resetHour, offsetSeconds)
}

function pad2(value: number): string {
  return String(value).padStart(2, '0')
}

/** YYYY-MM-DD HH:00 in server local time. */
export function formatResetHour(date: Date, offsetSeconds: number): string {
  const wall = serverWallClock(date.getTime(), offsetSeconds)
  return `${wall.getUTCFullYear()}-${pad2(wall.getUTCMonth() + 1)}-${pad2(
    wall.getUTCDate()
  )} ${pad2(wall.getUTCHours())}:00`
}

/** Day of the month (server local time) a unix timestamp (seconds) falls on, or null. */
export function resetDayOfMonth(
  timestamp: number | null | undefined,
  offsetSeconds: number
): number | null {
  if (!timestamp || timestamp <= 0) return null
  return serverWallClock(timestamp * 1000, offsetSeconds).getUTCDate()
}

export function getKeyQuotaLevel(
  percent: number | null | undefined,
  warnPercent: number,
  stopPercent: number
): KeyQuotaLevel {
  if (percent == null || !Number.isFinite(percent)) return 'normal'
  if (percent >= stopPercent) return 'stop'
  if (percent >= warnPercent) return 'warn'
  return 'normal'
}

export const KEY_QUOTA_LEVEL_INDICATOR_CLASS: Record<KeyQuotaLevel, string> = {
  normal: '[&_[data-slot=progress-indicator]]:bg-success',
  warn: '[&_[data-slot=progress-indicator]]:bg-warning',
  stop: '[&_[data-slot=progress-indicator]]:bg-destructive',
}
