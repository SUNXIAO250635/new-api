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

import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import { DEFAULT_GROUP } from '../constants'
import type { ApiKey, ApiKeyFormData } from '../types'

// ============================================================================
// Form Schema
// ============================================================================

export function getApiKeyFormSchema(t: TFunction, maxAutoGroups = 5) {
  const autoGroupLimit =
    Number.isInteger(maxAutoGroups) && maxAutoGroups > 0 ? maxAutoGroups : 5

  return z
    .object({
      name: z.string().min(1, t('Please enter a name')),
      remain_quota_dollars: z.number().optional(),
      expired_time: z.date().optional(),
      unlimited_quota: z.boolean(),
      model_limits: z.array(z.string()),
      allow_ips: z.string().optional(),
      groups: z.array(z.string()),
      allow_user_quota_query: z.boolean(),
      cross_group_retry: z.boolean().optional(),
      tokenCount: z.number().min(1).optional(),
    })
    .superRefine((data, ctx) => {
      if (data.groups.length === 0) {
        ctx.addIssue({
          code: 'custom',
          path: ['groups'],
          message: t('Select at least one group'),
        })
      }

      if (data.groups.length > autoGroupLimit) {
        ctx.addIssue({
          code: 'custom',
          path: ['groups'],
          message: t('Select at most {{max}} groups', {
            max: autoGroupLimit,
          }),
        })
      }

      if (new Set(data.groups).size !== data.groups.length) {
        ctx.addIssue({
          code: 'custom',
          path: ['groups'],
          message: t('Groups must not contain duplicates'),
        })
      }

      if (data.unlimited_quota) {
        return
      }

      if (
        data.remain_quota_dollars === undefined ||
        data.remain_quota_dollars < 0
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['remain_quota_dollars'],
          message: t('Quota must be zero or greater'),
        })
      }
    })
}

export type ApiKeyFormValues = z.infer<ReturnType<typeof getApiKeyFormSchema>>

// ============================================================================
// Form Defaults
// ============================================================================

export const API_KEY_FORM_DEFAULT_VALUES: ApiKeyFormValues = {
  name: '',
  remain_quota_dollars: 10,
  expired_time: undefined,
  unlimited_quota: true,
  model_limits: [],
  allow_ips: '',
  groups: [DEFAULT_GROUP],
  allow_user_quota_query: true,
  cross_group_retry: false,
  tokenCount: 1,
}

export function getApiKeyFormDefaultValues(
  defaultGroups: string[] = [DEFAULT_GROUP]
): ApiKeyFormValues {
  const groups = defaultGroups.length > 0 ? [...defaultGroups] : [DEFAULT_GROUP]
  return {
    ...API_KEY_FORM_DEFAULT_VALUES,
    groups,
    cross_group_retry: groups.length > 1,
  }
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: ApiKeyFormValues
): ApiKeyFormData {
  const useOrderedGroups = data.groups.length > 1
  return {
    name: data.name,
    remain_quota: data.unlimited_quota
      ? 0
      : parseQuotaFromDollars(data.remain_quota_dollars || 0),
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : -1,
    unlimited_quota: data.unlimited_quota,
    model_limits_enabled: data.model_limits.length > 0,
    model_limits: data.model_limits.join(','),
    allow_ips: data.allow_ips || '',
    allow_user_quota_query: data.allow_user_quota_query,
    group: useOrderedGroups ? 'auto' : data.groups[0] || '',
    auto_groups: useOrderedGroups ? data.groups : [],
    cross_group_retry: useOrderedGroups ? !!data.cross_group_retry : false,
  }
}

/**
 * Transform API key data to form defaults
 */
export function transformApiKeyToFormDefaults(
  apiKey: ApiKey,
  availableAutoGroups: string[] = [],
  maxAutoGroups = 5,
  inheritedAutoGroups: string[] = []
): ApiKeyFormValues {
  const availableSet = new Set(availableAutoGroups)
  const storedAutoGroups = apiKey.auto_groups ?? []
  let configuredGroups: string[]
  if (apiKey.group === 'auto') {
    configuredGroups =
      storedAutoGroups.length > 0 ? storedAutoGroups : inheritedAutoGroups
  } else {
    configuredGroups = [apiKey.group || DEFAULT_GROUP]
  }
  const groups = configuredGroups
    .filter((group) => availableSet.size === 0 || availableSet.has(group))
    .slice(0, Math.max(0, maxAutoGroups))

  return {
    name: apiKey.name,
    remain_quota_dollars: apiKey.unlimited_quota
      ? 0
      : quotaUnitsToDollars(apiKey.remain_quota),
    expired_time:
      apiKey.expired_time > 0
        ? new Date(apiKey.expired_time * 1000)
        : undefined,
    unlimited_quota: apiKey.unlimited_quota,
    model_limits: apiKey.model_limits
      ? apiKey.model_limits.split(',').filter(Boolean)
      : [],
    allow_ips: apiKey.allow_ips || '',
    groups,
    allow_user_quota_query: !!apiKey.allow_user_quota_query,
    cross_group_retry: !!apiKey.cross_group_retry,
    tokenCount: 1,
  }
}
