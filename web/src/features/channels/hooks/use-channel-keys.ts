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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback } from 'react'

import { getChannelKeys } from '../api'
import { channelsQueryKeys } from '../lib/channel-actions'

/** Keys of a channel with this period's usage (GET /api/channel/:id/keys). */
export function useChannelKeys(channelId: number | null, enabled = true) {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: channelsQueryKeys.keys(channelId ?? 0),
    queryFn: () => getChannelKeys(channelId as number),
    enabled: enabled && channelId !== null && channelId > 0,
  })

  /** Reload the keys and the channel list (status, usage summary). */
  const refresh = useCallback(() => {
    if (channelId === null) return
    void queryClient.invalidateQueries({
      queryKey: channelsQueryKeys.keys(channelId),
    })
    void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  }, [channelId, queryClient])

  return { ...query, refresh }
}
