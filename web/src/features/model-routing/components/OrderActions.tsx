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
import { ArrowDown, ArrowUp, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function OrderActions(props: {
  name: string
  atStart: boolean
  atEnd: boolean
  onMove: (offset: -1 | 1) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  return (
    <div className='flex items-center gap-1'>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        disabled={props.atStart}
        aria-label={t('Move {{name}} up', { name: props.name })}
        onClick={() => props.onMove(-1)}
      >
        <ArrowUp aria-hidden='true' />
      </Button>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        disabled={props.atEnd}
        aria-label={t('Move {{name}} down', { name: props.name })}
        onClick={() => props.onMove(1)}
      >
        <ArrowDown aria-hidden='true' />
      </Button>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        aria-label={t('Remove {{name}}', { name: props.name })}
        onClick={props.onRemove}
      >
        <Trash2 aria-hidden='true' />
      </Button>
    </div>
  )
}
