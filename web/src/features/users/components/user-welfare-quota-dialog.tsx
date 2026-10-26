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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import {
  formatQuota,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'

import { setUserWelfareQuota } from '../api'

const MAX_QUOTA = 2_147_483_647

interface UserWelfareQuotaDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  currentQuota: number
  currentExpiresAt: number
  onSuccess: (quota: number, expiresAt: number) => void
}

export function UserWelfareQuotaDialog(props: UserWelfareQuotaDialogProps) {
  const { t } = useTranslation()
  const [amount, setAmount] = useState('')
  const [expiresAt, setExpiresAt] = useState<Date>()
  const [loading, setLoading] = useState(false)
  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const maximumAmount = tokensOnly ? MAX_QUOTA : quotaUnitsToDollars(MAX_QUOTA)

  useEffect(() => {
    if (!props.open) return
    const displayAmount = tokensOnly
      ? String(props.currentQuota)
      : quotaUnitsToDollars(props.currentQuota).toFixed(6)
    setAmount(displayAmount)
    setExpiresAt(
      props.currentExpiresAt > 0
        ? new Date(props.currentExpiresAt * 1000)
        : undefined
    )
  }, [props.currentExpiresAt, props.currentQuota, props.open, tokensOnly])

  const handleConfirm = async () => {
    const amountValue = Number.parseFloat(amount)
    if (
      !Number.isFinite(amountValue) ||
      amountValue < 0 ||
      amountValue > maximumAmount
    ) {
      toast.error(t('Enter a valid non-negative amount'))
      return
    }
    const quota = parseQuotaFromDollars(amountValue)
    if (quota > MAX_QUOTA) {
      toast.error(t('Enter a valid non-negative amount'))
      return
    }
    const expiresAtSeconds = expiresAt
      ? Math.floor(expiresAt.getTime() / 1000)
      : 0
    if (
      quota > 0 &&
      expiresAtSeconds > 0 &&
      expiresAtSeconds <= Date.now() / 1000
    ) {
      toast.error(t('Expiration time must be in the future'))
      return
    }

    setLoading(true)
    try {
      const result = await setUserWelfareQuota({
        id: props.userId,
        action: 'set_welfare_quota',
        value: quota,
        expires_at: expiresAtSeconds,
      })
      if (!result.success) {
        toast.error(result.message || t('Failed to update welfare balance'))
        return
      }
      toast.success(t('Welfare balance updated successfully'))
      props.onSuccess(quota, expiresAtSeconds)
      props.onOpenChange(false)
    } catch (error: unknown) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to update welfare balance')
      )
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Set Welfare Balance')}
      description={t(
        'Welfare balance is consumed before subscriptions when it can cover the request.'
      )}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={loading}>
            {loading ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='text-muted-foreground text-sm'>
          {t('Current welfare balance')}: {formatQuota(props.currentQuota)}
        </div>
        <div className='space-y-2'>
          <Label>
            {t('Amount')} ({currencyLabel})
          </Label>
          <Input
            type='number'
            min={0}
            max={maximumAmount}
            step={tokensOnly ? 1 : 0.000001}
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label>{t('Expiration Time')}</Label>
          <DateTimePicker
            value={expiresAt}
            onChange={setExpiresAt}
            placeholder={t('Never expires')}
          />
        </div>
      </div>
    </Dialog>
  )
}
