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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getEnabledModels } from '@/features/channels/api'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { getModelRouting } from './api'
import { RoutingEditor } from './components/RoutingEditor'
import { ROUTING_ALIAS } from './types'

const policyQueryKey = ['model-routing'] as const

export function ModelRouting() {
  const { t } = useTranslation()
  const policy = useQuery({
    queryKey: policyQueryKey,
    queryFn: getModelRouting,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
  })
  const models = useQuery({
    queryKey: ['enabled-models'],
    queryFn: async () => requireServerSuccess(await getEnabledModels()),
    meta: { errorToast: false },
  })
  const modelOptions = useMemo(() => {
    const names = new Set(models.data?.data)
    names.delete(ROUTING_ALIAS)
    return [...names].sort().map((model) => ({ label: model, value: model }))
  }, [models.data])

  let content = <LoadingState />
  if (policy.data) {
    content = (
      <div className='grid max-w-5xl gap-4'>
        {models.isError && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('Could not load enabled models.')}{' '}
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => void models.refetch()}
                disabled={models.isFetching}
              >
                {t('Retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {models.isPending && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Loading models...')}
          </p>
        )}
        {models.isSuccess && modelOptions.length === 0 && (
          <Alert>
            <AlertDescription>
              {t('No enabled models are available for routing.')}
            </AlertDescription>
          </Alert>
        )}
        <RoutingEditor
          initial={policy.data}
          modelOptions={modelOptions}
          modelsReady={models.isSuccess}
          aliasConflict={models.data?.data?.includes(ROUTING_ALIAS) ?? false}
          onReload={async () => {
            void models.refetch()
            const result = await policy.refetch({ throwOnError: true })
            if (!result.data) {
              throw new Error(t('Failed to load model routing settings'))
            }
            return result.data
          }}
          policyQueryKey={policyQueryKey}
        />
      </div>
    )
  } else if (policy.isError) {
    content = (
      <ErrorState
        description={getServerErrorMessage(
          policy.error,
          t('Failed to load model routing settings')
        )}
        onRetry={() => void policy.refetch()}
      />
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model Routing')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
