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

import { GroupBadge } from '@/components/group-badge'
import { cn } from '@/lib/utils'

import { parseUsageGroups } from '../lib/usage-groups'

interface SubscriptionUsageGroupsProps {
  value?: string | null
  showAllGroups?: boolean
  className?: string
}

export function SubscriptionUsageGroups({
  value,
  showAllGroups = true,
  className,
}: SubscriptionUsageGroupsProps) {
  const { t } = useTranslation()
  const groups = parseUsageGroups(value)

  if (groups.length === 0) {
    return showAllGroups ? (
      <span className='text-muted-foreground'>{t('All Groups')}</span>
    ) : null
  }

  return (
    <span
      className={cn(
        'inline-flex min-w-0 flex-wrap items-center gap-1',
        className
      )}
    >
      {groups.map((group) => (
        <GroupBadge key={group} group={group} size='sm' />
      ))}
    </span>
  )
}
