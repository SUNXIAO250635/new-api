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

export const MAX_RULES = 32
export const MAX_CANDIDATES = 8
export const ROUTING_ALIAS = 'auto'

const condition = z.enum(['any', 'present', 'absent'])
const endpoint = z.enum(['chat', 'responses'])
const encoder = new TextEncoder()

export type ModelRoutingCondition = z.infer<typeof condition>
export type ModelRoutingEndpoint = z.infer<typeof endpoint>

export type ModelRoutingCandidate = {
  model: string
  endpoints: ModelRoutingEndpoint[]
  vision: boolean
  tools: boolean
}

export type ModelRoutingRule = {
  name: string
  image: ModelRoutingCondition
  tools: ModelRoutingCondition
  candidates: ModelRoutingCandidate[]
}

export type ModelRoutingPolicy = {
  enabled: boolean
  revision: number
  rules: ModelRoutingRule[]
}

export function modelRoutingSchema(
  t: TFunction
): z.ZodType<ModelRoutingPolicy, ModelRoutingPolicy> {
  const candidate = z.object({
    model: z
      .string()
      .refine(
        (model) =>
          model !== ROUTING_ALIAS &&
          model.length > 0 &&
          encoder.encode(model).length <= 255,
        t('Choose a concrete model with an ID of up to 255 bytes.')
      ),
    endpoints: z.array(endpoint).min(1, t('Select at least one endpoint.')),
    vision: z.boolean(),
    tools: z.boolean(),
  })
  const rule = z
    .object({
      name: z
        .string()
        .trim()
        .refine(
          (name) => name.length > 0 && [...name].length <= 128,
          t('Enter a rule name of up to 128 characters.')
        ),
      image: condition,
      tools: condition,
      candidates: z.array(candidate),
    })
    .superRefine((value, ctx) => {
      if (
        value.candidates.length < 1 ||
        value.candidates.length > MAX_CANDIDATES
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['candidates', 'root'],
          message: t('Choose between 1 and 8 candidates.'),
        })
      }
      if (
        new Set(value.candidates.map((item) => item.model)).size !==
        value.candidates.length
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['candidates', 'root'],
          message: t('Choose different models for each candidate in a rule.'),
        })
      }
    })

  return z
    .object({
      enabled: z.boolean(),
      revision: z.number().int().nonnegative(),
      rules: z.array(rule),
    })
    .superRefine((policy, ctx) => {
      const firstDefault = policy.rules.findIndex(isDefaultRule)
      let message: string | undefined
      if (policy.rules.length > MAX_RULES) {
        message = t('You can configure up to 32 rules.')
      } else if (firstDefault === -1 && policy.enabled) {
        message = t('Enabled routing requires a default rule.')
      } else if (firstDefault === -1 && policy.rules.length > 0) {
        message = t(
          'Model routing requires exactly one default rule at the end'
        )
      } else if (
        firstDefault !== -1 &&
        policy.rules.slice(firstDefault + 1).some(isDefaultRule)
      ) {
        message = t('Only one default rule is allowed.')
      } else if (
        firstDefault !== -1 &&
        firstDefault !== policy.rules.length - 1
      ) {
        message = t('The default rule must be last.')
      }
      if (message) {
        ctx.addIssue({ code: 'custom', path: ['rules', 'root'], message })
      }
    })
}

export function isDefaultRule(
  rule: Pick<ModelRoutingRule, 'image' | 'tools'>
): boolean {
  return rule.image === 'any' && rule.tools === 'any'
}
