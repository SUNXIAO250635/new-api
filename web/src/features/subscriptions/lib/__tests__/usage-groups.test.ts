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
import { expect, test } from 'vitest'

import type { SubscriptionPlan } from '../../types'
import { formValuesToPlanPayload, planToFormValues } from '../plan-form'
import { parseUsageGroups, serializeUsageGroups } from '../usage-groups'

function createPlan(usageGroup: string): SubscriptionPlan {
  return {
    id: 1,
    title: 'Multi group plan',
    price_amount: 1,
    currency: 'USD',
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'never',
    enabled: true,
    sort_order: 0,
    allow_balance_pay: true,
    allow_wallet_overflow: true,
    max_purchase_per_user: 0,
    total_amount: 1000,
    concurrency_limit: 3,
    usage_group: usageGroup,
  }
}

test('usage group helpers normalize legacy and multi-value strings', () => {
  expect(parseUsageGroups(' vip, default, vip ')).toEqual(['vip', 'default'])
  expect(serializeUsageGroups([' vip ', 'default', 'vip'])).toBe('vip,default')
})

test('plan form converts legacy usage_group to multi-select values', () => {
  const values = planToFormValues(createPlan('vip'))
  expect(values.usage_groups).toEqual(['vip'])
})

test('plan form serializes selected groups into the compatible API field', () => {
  const values = planToFormValues(createPlan('vip,default'))
  const parsed = formValuesToPlanPayload(values)
  expect(parsed.plan.usage_group).toBe('vip,default')
  expect('usage_groups' in parsed.plan).toBe(false)
})

test('plan form preserves the subscription concurrency limit', () => {
  const values = planToFormValues(createPlan('vip'))
  expect(values.concurrency_limit).toBe(3)

  const parsed = formValuesToPlanPayload({
    ...values,
    concurrency_limit: 5,
  })
  expect(parsed.plan.concurrency_limit).toBe(5)
})
