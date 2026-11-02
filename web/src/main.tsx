// SPDX-License-Identifier: AGPL-3.0-or-later
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { i18nReady } from './i18n'
import './styles/index.css'
import { App } from './App'
import { initTheme } from './lib/theme'

initTheme()

const root = document.getElementById('root')
if (!root) throw new Error('#root element missing from index.html')

// The first language's catalog may be a separate chunk (French): render once it's there.
void i18nReady.then(() =>
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  ),
)
