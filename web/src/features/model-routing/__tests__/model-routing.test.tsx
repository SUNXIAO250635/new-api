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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ModelRouting } from '..'
import type { ModelRoutingCandidate, ModelRoutingPolicy } from '../types'

const routingURL = '/api/model_routing'
const first: ModelRoutingCandidate = {
  model: 'atlas',
  endpoints: ['chat'],
  vision: false,
  tools: false,
}
const second: ModelRoutingCandidate = {
  model: 'boreal',
  endpoints: ['responses'],
  vision: true,
  tools: true,
}

let policy: ModelRoutingPolicy
let enabledModels: string[]
let client: QueryClient

beforeEach(() => {
  policy = {
    enabled: false,
    revision: 4,
    rules: [
      { name: 'Images', image: 'present', tools: 'any', candidates: [first] },
      { name: 'Tools', image: 'any', tools: 'present', candidates: [second] },
      {
        name: 'Everything else',
        image: 'any',
        tools: 'any',
        candidates: [first, second],
      },
    ],
  }
  enabledModels = ['atlas', 'boreal', 'cumulus']
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === routingURL) return { data: { success: true, data: policy } }
    if (url === '/api/channel/models_enabled') {
      return { data: { success: true, data: enabledModels } }
    }
    throw new Error(`Unexpected GET ${url}`)
  })
  vi.spyOn(api, 'put').mockImplementation(async (url, data) => {
    if (url !== routingURL) throw new Error(`Unexpected PUT ${url}`)
    const body = data as ModelRoutingPolicy
    if (body.revision !== policy.revision) {
      throw Object.assign(new Error('Conflict'), { response: { status: 409 } })
    }
    policy = { ...body, revision: policy.revision + 1 }
    return { data: { success: true, data: policy } }
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

async function openRouting() {
  render(
    <QueryClientProvider client={client}>
      <ModelRouting />
    </QueryClientProvider>
  )
  return {
    toggle: await screen.findByRole('switch', { name: 'Enable model routing' }),
    user: userEvent.setup(),
  }
}

function saveButton() {
  return screen.getByRole('button', { name: 'Save' })
}

test('stages ordered changes until Save, then reloads the persisted enable switch and priority', async () => {
  const { toggle, user } = await openRouting()
  await user.click(toggle)
  screen.getByRole('button', { name: 'Move Tools up' }).focus()
  await user.keyboard(' ')
  const defaults = within(
    screen.getByRole('group', { name: 'Everything else' })
  )
  await user.click(defaults.getByRole('button', { name: 'Move boreal up' }))
  expect(api.put).not.toHaveBeenCalled()

  await user.click(saveButton())
  await screen.findByText('Model routing saved.')
  expect(policy.enabled).toBe(true)
  expect(policy.rules.map((rule) => rule.name)).toEqual([
    'Tools',
    'Images',
    'Everything else',
  ])
  expect(
    policy.rules[2].candidates.map((candidate) => candidate.model)
  ).toEqual(['boreal', 'atlas'])

  await user.click(screen.getByRole('button', { name: 'Reload' }))
  expect(await screen.findByText('Model routing reloaded.')).toBeVisible()
  const savedToggle = screen.getByRole('switch', {
    name: 'Enable model routing',
  })
  expect(savedToggle).toBeChecked()
  expect(
    screen
      .getAllByRole('textbox', { name: 'Rule name' })
      .map((input) => (input as HTMLInputElement).value)
  ).toEqual(['Tools', 'Images', 'Everything else'])
  expect(
    within(screen.getByRole('group', { name: 'Everything else' })).getByRole(
      'button',
      { name: 'Move boreal up' }
    )
  ).toBeDisabled()

  await user.click(savedToggle)
  await user.click(saveButton())
  await screen.findByText('Model routing saved.')
  expect(policy.enabled).toBe(false)
})

test('rejects an enabled draft without a single final default rule', async () => {
  policy.rules = []
  const { toggle, user } = await openRouting()
  await user.click(toggle)
  await user.click(saveButton())
  expect(
    await screen.findByText('Enabled routing requires a default rule.')
  ).toBeVisible()

  await user.click(screen.getByRole('button', { name: 'Add default rule' }))
  await user.click(screen.getByRole('button', { name: 'Add rule' }))
  await user.click(screen.getByRole('button', { name: 'Move New rule down' }))
  expect(
    await screen.findByText('The default rule must be last.')
  ).toBeVisible()
  expect(api.put).not.toHaveBeenCalled()
})

test('adds an enabled model as a candidate and saves its endpoint and capabilities', async () => {
  enabledModels.push('auto')
  const { user } = await openRouting()
  const images = within(screen.getByRole('group', { name: 'Images' }))
  await user.selectOptions(
    images.getByRole('combobox', { name: 'Image input' }),
    'absent'
  )
  await user.click(images.getByRole('combobox', { name: 'Add candidate' }))
  expect(screen.queryByRole('option', { name: 'auto' })).not.toBeInTheDocument()
  expect(
    screen.queryByRole('option', { name: 'atlas' })
  ).not.toBeInTheDocument()
  await user.click(await screen.findByRole('option', { name: 'cumulus' }))
  const candidate = within(images.getByRole('group', { name: 'cumulus' }))
  await user.click(
    candidate.getByRole('checkbox', { name: 'Chat Completions' })
  )
  await user.click(saveButton())
  expect(await screen.findByText('Select at least one endpoint.')).toBeVisible()
  expect(
    candidate.getByRole('checkbox', { name: 'Chat Completions' })
  ).toHaveAttribute('aria-invalid', 'true')
  expect(api.put).not.toHaveBeenCalled()
  await user.click(candidate.getByRole('checkbox', { name: 'Responses' }))
  await user.click(candidate.getByRole('checkbox', { name: 'Supports images' }))
  await user.click(candidate.getByRole('checkbox', { name: 'Supports tools' }))
  await user.click(saveButton())
  await screen.findByText('Model routing saved.')
  expect(policy.rules[0]).toEqual({
    name: 'Images',
    image: 'absent',
    tools: 'any',
    candidates: [
      first,
      { model: 'cumulus', endpoints: ['responses'], vision: true, tools: true },
    ],
  })
})

test('keeps stale edits until confirmed reload, then permits saving against the new revision', async () => {
  const { user } = await openRouting()
  const name = screen.getByDisplayValue('Images')
  await user.clear(name)
  await user.type(name, 'Local edit')
  policy = {
    ...policy,
    revision: 5,
    rules: [
      { ...policy.rules[0], name: 'Remote edit' },
      ...policy.rules.slice(1),
    ],
  }

  await user.click(saveButton())
  expect(
    await screen.findByText(
      'The model routing settings have changed. Reload before saving again.'
    )
  ).toBeVisible()
  expect(name).toHaveValue('Local edit')
  expect(saveButton()).toBeDisabled()

  await user.click(screen.getByRole('button', { name: 'Reload' }))
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Discard unsaved changes?',
  })
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(name).toHaveValue('Local edit')
  await user.click(screen.getByRole('button', { name: 'Reload' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Reload',
    })
  )
  const remote = await screen.findByDisplayValue('Remote edit')
  await user.clear(remote)
  await user.type(remote, 'Updated edit')
  await user.click(saveButton())
  await screen.findByText('Model routing saved.')
  expect(policy.revision).toBe(6)
  expect(policy.rules[0].name).toBe('Updated edit')
})

test('removes rules and candidates from the draft before saving', async () => {
  const { user } = await openRouting()
  const defaults = within(
    screen.getByRole('group', { name: 'Everything else' })
  )
  await user.click(defaults.getByRole('button', { name: 'Remove atlas' }))
  await user.click(screen.getByRole('button', { name: 'Remove Images' }))
  expect(screen.queryByDisplayValue('Images')).not.toBeInTheDocument()
  expect(
    defaults.queryByRole('group', { name: 'atlas' })
  ).not.toBeInTheDocument()
  expect(api.put).not.toHaveBeenCalled()

  await user.click(saveButton())
  await screen.findByText('Model routing saved.')
  expect(policy.rules.map((rule) => rule.name)).toEqual([
    'Tools',
    'Everything else',
  ])
  expect(policy.rules[1].candidates).toEqual([second])
})
