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
import type { FieldErrors, UseFormRegisterReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ModelSelector } from '@/components/model-group-selector'
import { Badge } from '@/components/ui/badge'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  isDefaultRule,
  MAX_CANDIDATES,
  type ModelRoutingCondition,
  type ModelRoutingRule,
} from '../types'
import { CandidateEditor } from './CandidateEditor'
import { OrderActions } from './OrderActions'

type RuleEditorProps = {
  rule: ModelRoutingRule
  modelOptions: { label: string; value: string }[]
  modelsReady: boolean
  index: number
  count: number
  disabled: boolean
  errors?: FieldErrors<ModelRoutingRule>
  nameField: UseFormRegisterReturn
  onChange: (rule: ModelRoutingRule) => void
  onMove: (offset: -1 | 1) => void
  onRemove: () => void
}

export function RuleEditor(props: RuleEditorProps) {
  const { t } = useTranslation()
  const id = useId()
  const rule = props.rule
  const title = rule.name || t('Rule {{number}}', { number: props.index + 1 })
  const used = new Set(rule.candidates.map((candidate) => candidate.model))
  const available = props.modelsReady
    ? props.modelOptions.filter((option) => !used.has(option.value))
    : []
  const nameError = props.errors?.name?.message
  const candidateError = props.errors?.candidates?.root?.message
  const conditions = [
    { key: 'image', label: t('Image input') },
    { key: 'tools', label: t('Tool declarations') },
  ] as const

  function moveCandidate(index: number, offset: -1 | 1): void {
    const candidates = [...rule.candidates]
    const other = candidates[index + offset]
    candidates[index + offset] = candidates[index]
    candidates[index] = other
    props.onChange({ ...rule, candidates })
  }

  return (
    <FieldSet
      aria-label={title}
      className='bg-card min-w-0 gap-4 rounded-lg border p-3 sm:p-4'
    >
      <header className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <h3 className='font-medium [overflow-wrap:anywhere]'>{title}</h3>
          {isDefaultRule(rule) && (
            <Badge variant='secondary'>{t('Default rule')}</Badge>
          )}
        </div>
        <OrderActions
          name={title}
          atStart={props.index === 0}
          atEnd={props.index === props.count - 1}
          onMove={props.onMove}
          onRemove={props.onRemove}
        />
      </header>
      <FieldGroup className='grid gap-3 sm:grid-cols-3'>
        <Field data-invalid={Boolean(nameError)}>
          <FieldLabel htmlFor={`${id}-name`}>{t('Rule name')}</FieldLabel>
          <Input
            {...props.nameField}
            id={`${id}-name`}
            aria-required='true'
            aria-invalid={Boolean(nameError)}
            aria-describedby={nameError ? `${id}-name-error` : undefined}
          />
          <FieldError id={`${id}-name-error`}>{nameError}</FieldError>
        </Field>
        {conditions.map(({ key, label }) => (
          <Field key={key}>
            <FieldLabel htmlFor={`${id}-${key}`}>{label}</FieldLabel>
            <NativeSelect
              id={`${id}-${key}`}
              value={rule[key]}
              onChange={(event) =>
                props.onChange({
                  ...rule,
                  [key]: event.currentTarget.value as ModelRoutingCondition,
                })
              }
            >
              <NativeSelectOption value='any'>{t('Any')}</NativeSelectOption>
              <NativeSelectOption value='present'>
                {t('Present')}
              </NativeSelectOption>
              <NativeSelectOption value='absent'>
                {t('Absent')}
              </NativeSelectOption>
            </NativeSelect>
          </Field>
        ))}
      </FieldGroup>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h4 className='text-sm font-medium'>{t('Candidate models')}</h4>
        <ModelSelector
          selectedModel=''
          models={available}
          label={t('Add candidate')}
          showLabelOnMobile
          disabled={
            props.disabled ||
            !props.modelsReady ||
            available.length === 0 ||
            rule.candidates.length >= MAX_CANDIDATES
          }
          onModelChange={(model) =>
            props.onChange({
              ...rule,
              candidates: [
                ...rule.candidates,
                { model, endpoints: ['chat'], vision: false, tools: false },
              ],
            })
          }
        />
      </div>
      {rule.candidates.length === 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('No candidate models yet.')}
        </p>
      )}
      <FieldError>{candidateError}</FieldError>
      {rule.candidates.length > 0 && (
        <ol className='grid gap-2'>
          {rule.candidates.map((candidate, index) => {
            const enabled = props.modelOptions.find(
              (model) => model.value === candidate.model
            )
            return (
              <li key={candidate.model} className='min-w-0'>
                <CandidateEditor
                  candidate={candidate}
                  options={available}
                  index={index}
                  count={rule.candidates.length}
                  disabled={props.disabled}
                  unavailable={props.modelsReady && !enabled}
                  errors={props.errors?.candidates?.[index]}
                  onMove={(offset) => moveCandidate(index, offset)}
                  onRemove={() =>
                    props.onChange({
                      ...rule,
                      candidates: rule.candidates.filter(
                        (_, position) => position !== index
                      ),
                    })
                  }
                  onChange={(next) =>
                    props.onChange({
                      ...rule,
                      candidates: rule.candidates.map((item, position) =>
                        position === index ? next : item
                      ),
                    })
                  }
                />
              </li>
            )
          })}
        </ol>
      )}
    </FieldSet>
  )
}
