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
import { Activity, BarChart3, Gift, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import dayjs from '@/lib/dayjs'
import { formatQuota } from '@/lib/format'

import type { UserWalletData } from '../types'

interface WalletStatsCardProps {
  user: UserWalletData | null
  loading?: boolean
}

export function WalletStatsCard(props: WalletStatsCardProps) {
  const { t } = useTranslation()
  if (props.loading) {
    return (
      <div className='grid grid-cols-2 overflow-hidden rounded-lg border md:grid-cols-4'>
        {['balance', 'welfare', 'usage', 'requests'].map((key) => (
          <div
            key={key}
            className='min-w-0 border-b px-2.5 py-2.5 odd:border-r sm:px-5 sm:py-4 md:border-r md:border-b-0 md:last:border-r-0 [&:nth-child(n+3)]:border-b-0'
          >
            <Skeleton className='h-3.5 w-full' />
            <Skeleton className='mt-2 h-6 w-full sm:h-7' />
            <Skeleton className='mt-1.5 hidden h-3.5 w-24 md:block' />
          </div>
        ))}
      </div>
    )
  }

  const stats: {
    label: string
    value: string
    description?: string
    icon: typeof WalletCards
    tone: IconBadgeTone
  }[] = [
    {
      label: t('Current Balance'),
      value: formatQuota(props.user?.quota ?? 0),
      description: t('Remaining quota'),
      icon: WalletCards,
      tone: 'success',
    },
    {
      label: t('Welfare Balance'),
      value: formatQuota(props.user?.welfare_quota ?? 0),
      description:
        (props.user?.welfare_expires_at ?? 0) > 0
          ? t('Expires {{date}}', {
              date: dayjs
                .unix(props.user?.welfare_expires_at ?? 0)
                .format('YYYY-MM-DD HH:mm'),
            })
          : undefined,
      icon: Gift,
      tone: 'warning',
    },
    {
      label: t('Total Usage'),
      value: formatQuota(props.user?.used_quota ?? 0),
      description: t('Total consumed quota'),
      icon: BarChart3,
      tone: 'info',
    },
    {
      label: t('API Requests'),
      value: (props.user?.request_count ?? 0).toLocaleString(),
      description: t('Total requests made'),
      icon: Activity,
      tone: 'chart-4',
    },
  ]

  return (
    <div className='grid grid-cols-2 overflow-hidden rounded-lg border md:grid-cols-4'>
      {stats.map((item) => (
        <div
          key={item.label}
          className='min-w-0 border-b px-2.5 py-2.5 odd:border-r sm:px-5 sm:py-4 md:border-r md:border-b-0 md:last:border-r-0 [&:nth-child(n+3)]:border-b-0'
        >
          <div className='flex items-center gap-1.5 sm:gap-2.5'>
            <IconBadge tone={item.tone} size='stat'>
              <item.icon />
            </IconBadge>
            <div className='text-muted-foreground truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
              {item.label}
            </div>
          </div>

          <div className='text-foreground mt-1.5 font-mono text-sm font-bold tracking-tight break-all tabular-nums sm:mt-2.5 sm:text-2xl'>
            {item.value}
          </div>
          {item.description ? (
            <div className='text-muted-foreground/60 mt-1 hidden text-xs md:block'>
              {item.description}
            </div>
          ) : null}
        </div>
      ))}
    </div>
  )
}
