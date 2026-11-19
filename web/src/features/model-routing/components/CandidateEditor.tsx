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
import { useId } from 'react'
import type { FieldErrors } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ModelSelector } from '@/components/model-group-selector'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'

import type { ModelRoutingCandidate } from '../types'
import { OrderActions } from './OrderActions'

type CandidateEditorProps = {
  candidate: ModelRoutingCandidate
  options: { label: string; value: string }[]
  index: number
  count: number
  disabled: boolean
  unavailable: boolean
  errors?: FieldErrors<ModelRoutingCandidate>
  onChange: (candidate: ModelRoutingCandidate) => void
  onMove: (offset: -1 | 1) => void
  onRemove: () => void
}

export function CandidateEditor(props: CandidateEditorProps) {
  const { t } = useTranslation()
  const id = useId()
  const candidate = props.candidate
  const endpointError = props.errors?.endpoints?.message
  const endpoints = [
    { key: 'chat', label: t('Chat Completions') },
    { key: 'responses', label: t('Responses') },
  ] as const
  const capabilities = [
    { key: 'vision', label: t('Supports images') },
    { key: 'tools', label: t('Supports tools') },
  ] as const

  return (
    <FieldSet
      aria-label={candidate.model}
      className='min-w-0 gap-2 rounded-lg border px-3 py-2'
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <Badge variant='secondary'>
            {props.index === 0
              ? t('Primary model')
              : t('Fallback {{number}}', { number: props.index })}
          </Badge>
          <ModelSelector
            selectedModel={candidate.model}
            models={props.options}
            label={t('Change candidate {{model}}', { model: candidate.model })}
            showLabelOnMobile
            className='max-w-full'
            disabled={props.disabled || props.options.length === 0}
            onModelChange={(model) => props.onChange({ ...candidate, model })}
          />
          {props.unavailable && (
            <Badge variant='outline'>{t('Model not enabled')}</Badge>
          )}
        </div>
        <OrderActions
          name={candidate.model}
          atStart={props.index === 0}
          atEnd={props.index === props.count - 1}
          onMove={props.onMove}
          onRemove={props.onRemove}
        />
      </div>
      <FieldError>{props.errors?.model?.message}</FieldError>
      <FieldGroup className='flex-row flex-wrap gap-x-5 gap-y-2'>
        {endpoints.map(({ key, label }) => (
          <Field
            key={key}
            orientation='horizontal'
            className='w-auto'
            data-invalid={Boolean(endpointError)}
          >
            <Checkbox
              id={`${id}-${key}`}
              disabled={props.disabled}
              checked={candidate.endpoints.includes(key)}
              aria-invalid={Boolean(endpointError)}
              aria-describedby={
                endpointError ? `${id}-endpoint-error` : undefined
              }
              onCheckedChange={(checked) =>
                props.onChange({
                  ...candidate,
                  endpoints: checked
                    ? [...candidate.endpoints, key]
                    : candidate.endpoints.filter((item) => item !== key),
                })
              }
            />
            <FieldLabel htmlFor={`${id}-${key}`} className='font-normal'>
              {label}
            </FieldLabel>
          </Field>
        ))}
        {capabilities.map(({ key, label }) => (
          <Field key={key} orientation='horizontal' className='w-auto'>
            <Checkbox
              id={`${id}-${key}`}
              disabled={props.disabled}
              checked={candidate[key]}
              onCheckedChange={(checked) =>
                props.onChange({ ...candidate, [key]: checked })
              }
            />
            <FieldLabel htmlFor={`${id}-${key}`} className='font-normal'>
              {label}
            </FieldLabel>
          </Field>
        ))}
      </FieldGroup>
      <FieldError id={`${id}-endpoint-error`}>{endpointError}</FieldError>
    </FieldSet>
  )
}
