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
import { expect, test } from 'vitest'

import type { Redemption } from '../../types'
import {
  getRedemptionFormSchema,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from '../redemption-form'

const t = ((key: string) => key) as TFunction

test('subscription redemption requires a selected plan', () => {
  const result = getRedemptionFormSchema(t).safeParse({
    name: 'Monthly plan',
    type: 'subscription',
    quota_dollars: 0,
    plan_id: 0,
    allow_same_user: false,
    count: 1,
  })

  expect(result.success).toBe(false)
  if (!result.success) {
    expect(result.error.issues[0]?.path[0]).toBe('plan_id')
  }
})

test('subscription redemption payload grants the plan without quota', () => {
  const payload = transformFormDataToPayload({
    name: 'Monthly plan',
    type: 'subscription',
    quota_dollars: 99,
    plan_id: 7,
    allow_same_user: false,
    count: 3,
  })

  expect(payload.type).toBe('subscription')
  expect(payload.plan_id).toBe(7)
  expect(payload.quota).toBe(0)
  expect(payload.allow_same_user).toBe(false)
})

test('welfare redemption payload grants welfare quota without a plan', () => {
  const welfareExpiresAt = new Date('2030-01-02T00:00:00Z')
  const payload = transformFormDataToPayload({
    name: 'Welfare credit',
    type: 'welfare',
    quota_dollars: 25,
    plan_id: 9,
    allow_same_user: true,
    welfare_expires_at: welfareExpiresAt,
    count: 2,
  })

  expect(payload.type).toBe('welfare')
  expect(payload.quota).toBeGreaterThan(0)
  expect(payload.plan_id).toBeUndefined()
  expect(payload.allow_same_user).toBe(true)
  expect(payload.welfare_expires_at).toBe(
    Math.floor(welfareExpiresAt.getTime() / 1000)
  )
})

test('welfare balance expiration must be after code expiration', () => {
  const result = getRedemptionFormSchema(t).safeParse({
    name: 'Timed welfare',
    type: 'welfare',
    quota_dollars: 10,
    plan_id: 0,
    allow_same_user: false,
    expired_time: new Date('2030-01-02T00:00:00Z'),
    welfare_expires_at: new Date('2030-01-01T00:00:00Z'),
    count: 1,
  })

  expect(result.success).toBe(false)
  if (!result.success) {
    expect(
      result.error.issues.some(
        (issue) => issue.path[0] === 'welfare_expires_at'
      )
    ).toBe(true)
  }
})

test('legacy redemption without a type defaults to quota redemption', () => {
  const redemption: Redemption = {
    id: 1,
    user_id: 1,
    name: 'Legacy code',
    key: 'legacy-key',
    status: 1,
    quota: 500000,
    created_time: 1,
    redeemed_time: 0,
    expired_time: 0,
    used_user_id: 0,
  }

  const defaults = transformRedemptionToFormDefaults(redemption)

  expect(defaults.type).toBe('quota')
  expect(defaults.plan_id).toBe(0)
  expect(defaults.allow_same_user).toBe(false)
})
