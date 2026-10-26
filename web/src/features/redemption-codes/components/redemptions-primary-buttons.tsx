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
import { getRouteApi } from '@tanstack/react-router'
import { Download, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteInvalidRedemptions, exportRedemptions } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { useRedemptions } from './redemptions-provider'

const route = getRouteApi('/_authenticated/redemption-codes/')

export function RedemptionsPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, triggerRefresh } = useRedemptions()
  const [showDeleteInvalidConfirm, setShowDeleteInvalidConfirm] =
    useState(false)
  const [isDeleting, setIsDeleting] = useState(false)
  const [isExporting, setIsExporting] = useState(false)

  const search = route.useSearch()

  const handleDeleteInvalid = async () => {
    setIsDeleting(true)
    try {
      const result = await deleteInvalidRedemptions()
      if (result.success) {
        const count = result.data || 0
        toast.success(
          t('Successfully deleted {{count}} invalid redemption codes', {
            count,
          })
        )
        triggerRefresh()
        setShowDeleteInvalidConfirm(false)
      } else {
        handleServerError(result, t(ERROR_MESSAGES.DELETE_INVALID_FAILED))
      }
    } catch {
      toast.error(t(ERROR_MESSAGES.DELETE_INVALID_FAILED))
    } finally {
      setIsDeleting(false)
    }
  }

  const handleExport = async () => {
    setIsExporting(true)
    try {
      const blob = await exportRedemptions({
        keyword: search.filter,
        status: search.status?.[0],
        type: search.type?.[0],
      })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = 'redemption-codes.txt'
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      toast.success(t('Redemption codes exported successfully'))
    } catch {
      toast.error(t('Failed to export redemption codes'))
    } finally {
      setIsExporting(false)
    }
  }

  return (
    <>
      <div className='flex flex-wrap gap-2'>
        <Button
          size='sm'
          variant='outline'
          onClick={() => void handleExport()}
          disabled={isExporting}
        >
          <Download className='h-4 w-4' />
          {isExporting ? t('Exporting...') : t('Export TXT')}
        </Button>
        <Button
          size='sm'
          variant='outline'
          onClick={() => setShowDeleteInvalidConfirm(true)}
        >
          <Trash2 className='text-destructive h-4 w-4' />
          {t('Delete Invalid')}
        </Button>
        <Button size='sm' onClick={() => setOpen('create')}>
          <Plus className='h-4 w-4' />
          {t('Create Code')}
        </Button>
      </div>

      <ConfirmDialog
        destructive
        open={showDeleteInvalidConfirm}
        onOpenChange={setShowDeleteInvalidConfirm}
        handleConfirm={handleDeleteInvalid}
        isLoading={isDeleting}
        className='max-w-md'
        title={t('Delete Invalid Redemption Codes?')}
        desc={
          <>
            <Trans
              t={t}
              i18nKey='This will delete all <strong>used</strong>, <strong>disabled</strong>, and <strong>expired</strong> redemption codes.'
              components={{ strong: <strong /> }}
            />
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete Invalid')}
      />
    </>
  )
}
