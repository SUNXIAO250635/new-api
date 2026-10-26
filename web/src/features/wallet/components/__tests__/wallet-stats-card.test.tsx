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
import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import type { UserWalletData } from '../../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { WalletStatsCard } = await import('../wallet-stats-card')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Never expires': 'Never expires',
        'Expires {{date}}': 'Expires {{date}}',
      },
    },
  },
})

const user: UserWalletData = {
  id: 1,
  username: 'wallet-user',
  quota: 100,
  welfare_quota: 50,
  welfare_expires_at: 0,
  used_quota: 25,
  request_count: 3,
  aff_quota: 0,
  aff_history_quota: 0,
  aff_count: 0,
  group: 'default',
}

function renderCard(walletUser: UserWalletData) {
  return render(
    <I18nextProvider i18n={i18n}>
      <WalletStatsCard user={walletUser} />
    </I18nextProvider>
  )
}

describe('wallet welfare balance expiration', () => {
  test('does not show an expiration subtitle for permanent welfare balance', () => {
    renderCard(user)

    expect(screen.queryByText('Never expires')).not.toBeInTheDocument()
  })

  test('shows the expiration subtitle when welfare balance is time-limited', () => {
    renderCard({ ...user, welfare_expires_at: 1_893_456_000 })

    expect(screen.getByText(/^Expires /)).toBeInTheDocument()
  })
})
