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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToEditableAmount } from '@/lib/format'

import {
  REDEMPTION_TYPE,
  REDEMPTION_VALIDATION,
  getRedemptionFormErrorMessages,
} from '../constants'
import type { RedemptionFormData, Redemption } from '../types'

// ============================================================================
// Form Schema (use getRedemptionFormSchema(t) in components for i18n messages)
// ============================================================================

export function getRedemptionFormSchema(t: TFunction) {
  const msg = getRedemptionFormErrorMessages(t)
  return z
    .object({
      name: z
        .string()
        .min(REDEMPTION_VALIDATION.NAME_MIN_LENGTH, msg.NAME_LENGTH_INVALID)
        .max(REDEMPTION_VALIDATION.NAME_MAX_LENGTH, msg.NAME_LENGTH_INVALID),
      type: z.enum([
        REDEMPTION_TYPE.QUOTA,
        REDEMPTION_TYPE.WELFARE,
        REDEMPTION_TYPE.SUBSCRIPTION,
      ]),
      quota_dollars: z.number(),
      plan_id: z.number(),
      allow_same_user: z.boolean(),
      expired_time: z.date().optional(),
      welfare_expires_at: z.date().optional(),
      count: z
        .number()
        .min(REDEMPTION_VALIDATION.COUNT_MIN, msg.COUNT_INVALID)
        .max(REDEMPTION_VALIDATION.COUNT_MAX, msg.COUNT_INVALID)
        .optional(),
    })
    .superRefine((data, context) => {
      if (data.type === REDEMPTION_TYPE.SUBSCRIPTION) {
        if (data.plan_id <= 0) {
          context.addIssue({
            code: 'custom',
            path: ['plan_id'],
            message: t('Please select a subscription plan'),
          })
        }
        return
      }
      if (data.quota_dollars <= 0) {
        context.addIssue({
          code: 'custom',
          path: ['quota_dollars'],
          message: t('Quota must be a positive number'),
        })
      }
      if (data.type === REDEMPTION_TYPE.WELFARE && data.welfare_expires_at) {
        if (data.welfare_expires_at.getTime() <= Date.now()) {
          context.addIssue({
            code: 'custom',
            path: ['welfare_expires_at'],
            message: t('Welfare balance expiration must be in the future'),
          })
          return
        }
        if (
          !data.expired_time ||
          data.welfare_expires_at.getTime() <= data.expired_time.getTime()
        ) {
          context.addIssue({
            code: 'custom',
            path: ['welfare_expires_at'],
            message: t(
              'Welfare balance expiration must be after the redemption code expiration'
            ),
          })
        }
      }
    })
}

export type RedemptionFormValues = {
  name: string
  type: RedemptionFormData['type']
  quota_dollars: number
  plan_id: number
  allow_same_user: boolean
  expired_time?: Date
  welfare_expires_at?: Date
  count?: number
}

// ============================================================================
// Form Defaults
// ============================================================================

export const REDEMPTION_FORM_DEFAULT_VALUES: RedemptionFormValues = {
  name: '',
  type: REDEMPTION_TYPE.QUOTA,
  quota_dollars: 10,
  plan_id: 0,
  allow_same_user: false,
  expired_time: undefined,
  welfare_expires_at: undefined,
  count: 1,
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: RedemptionFormValues
): RedemptionFormData {
  let welfareExpiresAt: number | undefined
  if (data.type === REDEMPTION_TYPE.WELFARE) {
    welfareExpiresAt = data.welfare_expires_at
      ? Math.floor(data.welfare_expires_at.getTime() / 1000)
      : 0
  }

  return {
    name: data.name,
    type: data.type,
    quota:
      data.type !== REDEMPTION_TYPE.SUBSCRIPTION
        ? parseQuotaFromDollars(data.quota_dollars)
        : 0,
    plan_id:
      data.type === REDEMPTION_TYPE.SUBSCRIPTION ? data.plan_id : undefined,
    allow_same_user: data.allow_same_user,
    welfare_expires_at: welfareExpiresAt,
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : 0,
    count: data.count || 1,
  }
}

/**
 * Transform redemption data to form defaults
 */
export function transformRedemptionToFormDefaults(
  redemption: Redemption
): RedemptionFormValues {
  return {
    name: redemption.name,
    type: redemption.type ?? REDEMPTION_TYPE.QUOTA,
    quota_dollars: quotaUnitsToEditableAmount(redemption.quota),
    plan_id: redemption.plan_id ?? 0,
    allow_same_user: redemption.allow_same_user ?? false,
    welfare_expires_at:
      (redemption.welfare_expires_at ?? 0) > 0
        ? new Date((redemption.welfare_expires_at ?? 0) * 1000)
        : undefined,
    expired_time:
      redemption.expired_time > 0
        ? new Date(redemption.expired_time * 1000)
        : undefined,
    count: 1,
  }
}
