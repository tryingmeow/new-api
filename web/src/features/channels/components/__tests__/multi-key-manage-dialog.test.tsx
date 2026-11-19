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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { useEffect, useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { channelsQueryKeys } from '../../lib'
import {
  channelSchema,
  type Channel,
  type ChannelKeyRow,
  type KeyStatus,
} from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { MultiKeyManageDialog } from '../dialogs/multi-key-manage-dialog'

// Every test answers the keys request, so none reaches the network and
// signs out the next test's user when it fails late.
beforeEach(() => {
  mockChannelKeys()
})

afterEach(async () => {
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
  await i18next.changeLanguage('en')
})

const multiKeyChannel = {
  id: 7,
  type: 61,
  key: '',
  name: 'Account Live',
  status: 1,
  created_time: 1,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  channel_info: {
    is_multi_key: true,
    multi_key_size: 2,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  },
}

function keyStatusResponse(keys: KeyStatus[]) {
  const enabled = keys.filter((key) => key.status === 1).length
  return {
    data: {
      success: true,
      data: {
        keys,
        total: keys.length,
        page: 1,
        page_size: 10,
        total_pages: 1,
        enabled_count: enabled,
        manual_disabled_count: 0,
        auto_disabled_count: keys.length - enabled,
      },
    },
  }
}

/** Answers GET /api/channel/:id/keys, which the dialog loads beside the key status. */
function mockChannelKeys(keys: ChannelKeyRow[] = [], quota = null) {
  return vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url.endsWith('/keys')) {
      return {
        data: { success: true, data: { is_multi_key: true, quota, keys } },
      }
    }
    throw new Error(`unexpected GET ${url}`)
  })
}

function OpenDialog(props: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return <MultiKeyManageDialog open onOpenChange={() => {}} />
}

test('translates the disabled reasons the server writes and shows upstream text as written', async () => {
  i18next.addResourceBundle('zhCN', 'translation', {
    'Plugin credential needs a new login': '插件凭证需要重新登录',
  })
  await i18next.changeLanguage('zhCN')
  const upstream = 'upstream said {{bad}}'
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        keys: [
          {
            index: 0,
            status: 3,
            disabled_time: 1,
            reason: 'Plugin credential needs a new login',
          },
          { index: 1, status: 3, disabled_time: 1, reason: upstream },
        ],
        total: 2,
        page: 1,
        page_size: 10,
        total_pages: 1,
        enabled_count: 0,
        manual_disabled_count: 0,
        auto_disabled_count: 2,
      },
    },
  })
  const channel = channelSchema.parse(multiKeyChannel)

  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  expect(await screen.findByText('插件凭证需要重新登录')).toBeInTheDocument()
  expect(screen.getByText(upstream)).toBeInTheDocument()
})

/** Opens Manage Keys from inside another dialog, as the channel drawer does. */
function ManageKeysInsideDialog(props: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  const [editOpen, setEditOpen] = useState(true)
  const [manageOpen, setManageOpen] = useState(false)
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return (
    <Dialog open={editOpen} onOpenChange={setEditOpen} title='Edit Channel'>
      <Button onClick={() => setManageOpen(true)}>Manage Keys</Button>
      <MultiKeyManageDialog open={manageOpen} onOpenChange={setManageOpen} />
    </Dialog>
  )
}

test('after a key is deleted, Escape closes Manage Keys and not the dialog that opened it', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const post = vi.spyOn(api, 'post')
  post
    .mockResolvedValueOnce(
      keyStatusResponse([
        { index: 0, status: 1 },
        { index: 1, status: 1 },
      ])
    )
    .mockResolvedValueOnce({ data: { success: true, message: '' } })
    .mockResolvedValueOnce(keyStatusResponse([{ index: 0, status: 1 }]))
  const queryClient = new QueryClient()
  const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
  const user = userEvent.setup()

  render(
    <QueryClientProvider client={queryClient}>
      <ChannelsProvider>
        <ManageKeysInsideDialog
          channel={channelSchema.parse(multiKeyChannel)}
        />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Manage Keys' }))
  const manage = await screen.findByRole('dialog', {
    name: /Multi-Key Management/,
  })
  const row = await within(manage).findByRole('row', { name: /^#2\s/ })
  await user.click(within(row).getByRole('button', { name: 'Delete' }))
  await user.click(await screen.findByRole('button', { name: 'Continue' }))

  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(within(manage).queryByText('#2')).not.toBeInTheDocument()
  expect(post).toHaveBeenNthCalledWith(
    2,
    '/api/channel/multi_key/manage',
    { channel_id: 7, action: 'delete_key', key_index: 1 },
    expect.anything()
  )
  // The credential panel in the edit drawer shows the lines that are left.
  expect(invalidate).toHaveBeenCalledWith({
    queryKey: channelsQueryKeys.pluginCredentials(7),
  })
  await waitFor(() =>
    expect(manage.contains(document.activeElement)).toBe(true)
  )

  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: /Multi-Key Management/ })
    ).not.toBeInTheDocument()
  )
  expect(screen.getByRole('dialog', { name: 'Edit Channel' })).toBeVisible()
})

test('a plugin channel shows each key its own balance', async () => {
  vi.spyOn(api, 'post').mockResolvedValue(
    keyStatusResponse([
      { index: 0, status: 1 },
      { index: 1, status: 1 },
      { index: 2, status: 1 },
    ])
  )
  const channel = channelSchema.parse({
    ...multiKeyChannel,
    channel_info: { ...multiKeyChannel.channel_info, multi_key_size: 3 },
    balance_detail: {
      amount: null,
      currency: 'USD',
      unlimited: false,
      windows: [],
      keys: {
        '0': { amount: 42, currency: 'CNY', unlimited: false, windows: [] },
        '1': {
          amount: null,
          currency: 'USD',
          unlimited: false,
          windows: [{ name: 'Daily', used: 30, limit: 40 }],
          error: 'Key rejected by upstream',
        },
      },
    },
  })

  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channel} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const first = await screen.findByRole('row', { name: /^#1\s/ })
  expect(within(first).getByText('CN¥42')).toBeInTheDocument()
  const second = screen.getByRole('row', { name: /^#2\s/ })
  expect(within(second).getByText('75% used')).toBeInTheDocument()
  expect(within(second).getByText('Daily: 30 / 40')).toBeInTheDocument()
  expect(
    within(second).getByText('Last error: Key rejected by upstream')
  ).toBeInTheDocument()
  const balanceColumn = screen
    .getAllByRole('columnheader')
    .findIndex((header) => header.textContent === 'Balance')
  const third = screen.getByRole('row', { name: /^#3\s/ })
  expect(within(third).getAllByRole('cell')[balanceColumn]).toHaveTextContent(
    /^-$/
  )
})

test('a quota-paused key shows its name, own cap and a resume action that warns about charges', async () => {
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        keys: [{ index: 0, status: 4 }],
        total: 1,
        page: 1,
        page_size: 10,
        total_pages: 1,
        enabled_count: 0,
        manual_disabled_count: 0,
        auto_disabled_count: 0,
      },
    },
  })
  const quota = {
    enabled: true,
    monthly_quota: 5_000_000,
    reset_day: 5,
    reset_hour: 0,
    stop_percent: 90,
    warn_percent: 80,
  }
  mockChannelKeys([
    {
      index: 0,
      fingerprint: 'abcd1234abcd1234',
      preview: 'sk-ant-…f3c9',
      name: 'Card 1234',
      status: 4,
      quota_mode: 'custom',
      effective_quota: quota,
      used_quota: 4_600_000,
      request_count: 12,
      next_reset_at: new Date(2026, 11, 5).getTime() / 1000,
      percent: 92,
    },
  ])
  const user = userEvent.setup()

  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChannelsProvider>
        <OpenDialog channel={channelSchema.parse(multiKeyChannel)} />
      </ChannelsProvider>
    </QueryClientProvider>
  )

  const row = await screen.findByRole('row', { name: /Card 1234/ })
  expect(within(row).getByText('Own quota')).toBeInTheDocument()
  expect(within(row).getByText('sk-ant-…f3c9')).toBeInTheDocument()
  expect(within(row).getByText('2026-12-05 00:00')).toBeInTheDocument()
  expect(
    within(row).queryByRole('button', { name: 'Enable' })
  ).not.toBeInTheDocument()

  await user.click(within(row).getByRole('button', { name: 'Open menu' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Resume now' }))

  const confirm = await screen.findByRole('alertdialog')
  expect(confirm).toHaveTextContent(/charge your card/)
})
