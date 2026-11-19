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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useId, useMemo, useRef, useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import {
  getServerErrorMessage,
  getServerErrorStatus,
} from '@/lib/server-error-message'

import { saveModelRouting } from '../api'
import {
  isDefaultRule,
  MAX_RULES,
  modelRoutingSchema,
  type ModelRoutingPolicy,
} from '../types'
import { RuleEditor } from './RuleEditor'

type RoutingEditorProps = {
  initial: ModelRoutingPolicy
  modelOptions: { label: string; value: string }[]
  modelsReady: boolean
  onReload: () => Promise<ModelRoutingPolicy>
  aliasConflict: boolean
  policyQueryKey: readonly string[]
}

export function RoutingEditor(props: RoutingEditorProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const enabledId = useId()
  const reloadButton = useRef<HTMLButtonElement>(null)
  const errorSummary = useRef<HTMLDivElement>(null)
  const [reloadOpen, setReloadOpen] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [reloadError, setReloadError] = useState<unknown>(null)
  const [status, setStatus] = useState<'saved' | 'reloaded' | null>(null)
  const resolver = useMemo(() => zodResolver(modelRoutingSchema(t)), [t])
  const form = useForm<ModelRoutingPolicy>({
    defaultValues: props.initial,
    resolver,
    shouldFocusError: false,
  })
  const { errors, isDirty, isSubmitted, isSubmitting } = form.formState
  const { fields, insert, move, remove } = useFieldArray({
    control: form.control,
    name: 'rules',
  })
  const rules = useWatch({ control: form.control, name: 'rules' })
  const enabled = useWatch({ control: form.control, name: 'enabled' })
  const defaultIndex = rules.findIndex(isDefaultRule)
  const mutation = useMutation({
    mutationFn: saveModelRouting,
    meta: { errorToast: false },
    onSuccess: (saved) => {
      queryClient.setQueryData(props.policyQueryKey, saved)
      form.reset(saved)
      setStatus('saved')
    },
  })
  const stale = getServerErrorStatus(mutation.error) === 409
  const busy = reloading || mutation.isPending || isSubmitting
  const validationError = Object.keys(errors).length > 0

  function changed(): void {
    setStatus(null)
    if (isSubmitted) void form.trigger()
  }

  function addRule(defaultRule: boolean): void {
    const index =
      defaultRule || defaultIndex === -1 ? rules.length : defaultIndex
    insert(index, {
      name: defaultRule ? t('Default rule') : t('New rule'),
      image: defaultRule ? 'any' : 'present',
      tools: 'any',
      candidates: [],
    })
    changed()
  }

  async function reload(): Promise<void> {
    setReloadOpen(false)
    setReloading(true)
    setReloadError(null)
    setStatus(null)
    try {
      const fresh = await props.onReload()
      form.reset(fresh)
      mutation.reset()
      setStatus('reloaded')
    } catch (error) {
      setReloadError(error)
    } finally {
      setReloading(false)
    }
  }

  const submit = form.handleSubmit(
    (draft) => {
      if (!stale && !busy && isDirty) {
        setStatus(null)
        setReloadError(null)
        mutation.mutate(draft)
      }
    },
    () => requestAnimationFrame(() => errorSummary.current?.focus())
  )

  return (
    <form onSubmit={submit} noValidate>
      <FieldSet disabled={busy} className='min-w-0'>
        <FieldGroup className='gap-4'>
          <Field
            orientation='horizontal'
            className='bg-card justify-between rounded-lg border p-3 sm:p-4'
          >
            <FieldContent>
              <FieldLabel htmlFor={enabledId}>
                {t('Enable model routing')}
              </FieldLabel>
              <FieldDescription id={`${enabledId}-description`}>
                {t(
                  'Apply these rules to requests for the auto model. This does not configure Auto groups.'
                )}
              </FieldDescription>
            </FieldContent>
            <Switch
              id={enabledId}
              aria-describedby={`${enabledId}-description`}
              checked={enabled}
              disabled={busy}
              onCheckedChange={(checked) => {
                form.setValue('enabled', checked, { shouldDirty: true })
                changed()
              }}
            />
          </Field>
          <div className='flex flex-wrap items-center gap-2'>
            <Button type='submit' disabled={!isDirty || stale || busy}>
              {mutation.isPending ? t('Saving...') : t('Save')}
            </Button>
            <Button
              type='button'
              variant='outline'
              ref={reloadButton}
              disabled={busy}
              onClick={() => (isDirty ? setReloadOpen(true) : void reload())}
            >
              {reloading ? t('Reloading...') : t('Reload')}
            </Button>
            {status && (
              <p className='text-muted-foreground text-sm' role='status'>
                {status === 'saved'
                  ? t('Model routing saved.')
                  : t('Model routing reloaded.')}
              </p>
            )}
          </div>
          {validationError && (
            <Alert variant='destructive' ref={errorSummary} tabIndex={-1}>
              <AlertDescription>
                {errors.rules?.root?.message ||
                  t('Fix the highlighted routing fields before saving.')}
              </AlertDescription>
            </Alert>
          )}
          {props.aliasConflict && !mutation.isError && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t(
                  'An enabled model named auto already exists. Disable or rename it before enabling model routing.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {mutation.isError && (
            <Alert variant='destructive'>
              <AlertDescription>
                {stale
                  ? t(
                      'The model routing settings have changed. Reload before saving again.'
                    )
                  : getServerErrorMessage(
                      mutation.error,
                      t('Failed to save model routing settings')
                    )}
              </AlertDescription>
            </Alert>
          )}
          {reloadError != null && (
            <Alert variant='destructive'>
              <AlertDescription>
                {getServerErrorMessage(
                  reloadError,
                  t('Failed to load model routing settings')
                )}
              </AlertDescription>
            </Alert>
          )}
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div>
              <h3 className='font-medium'>{t('Routing rules')}</h3>
              <p className='text-muted-foreground text-sm'>
                {t('The first matching rule wins. Put the default rule last.')}
              </p>
            </div>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                disabled={fields.length >= MAX_RULES}
                onClick={() => addRule(false)}
              >
                <Plus aria-hidden='true' />
                {t('Add rule')}
              </Button>
              {defaultIndex === -1 && (
                <Button
                  type='button'
                  variant='outline'
                  disabled={fields.length >= MAX_RULES}
                  onClick={() => addRule(true)}
                >
                  <Plus aria-hidden='true' />
                  {t('Add default rule')}
                </Button>
              )}
            </div>
          </div>
          {fields.length === 0 && (
            <EmptyState title={t('No routing rules yet')} />
          )}
          <ol className='grid gap-3'>
            {fields.map((field, index) => {
              const rule = rules[index]
              if (!rule) return null
              return (
                <li key={field.id} className='min-w-0'>
                  <RuleEditor
                    rule={rule}
                    modelOptions={props.modelOptions}
                    modelsReady={props.modelsReady}
                    index={index}
                    count={rules.length}
                    disabled={busy}
                    nameField={form.register(`rules.${index}.name`, {
                      onChange: () => setStatus(null),
                    })}
                    errors={errors.rules?.[index]}
                    onChange={(next) => {
                      form.setValue(`rules.${index}`, next, {
                        shouldDirty: true,
                      })
                      changed()
                    }}
                    onMove={(offset) => {
                      move(index, index + offset)
                      changed()
                    }}
                    onRemove={() => {
                      remove(index)
                      changed()
                    }}
                  />
                </li>
              )
            })}
          </ol>
        </FieldGroup>
      </FieldSet>
      <ConfirmDialog
        open={reloadOpen}
        onOpenChange={setReloadOpen}
        title={t('Discard unsaved changes?')}
        desc={t(
          'Reloading replaces your draft with the settings currently saved on the server.'
        )}
        confirmText={t('Reload')}
        destructive
        finalFocus={reloadButton}
        handleConfirm={() => void reload()}
      />
    </form>
  )
}
