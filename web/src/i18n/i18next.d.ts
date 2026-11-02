// SPDX-License-Identifier: AGPL-3.0-or-later
// Typed translation keys: t('…') only accepts keys that exist in en.json.
import type en from './en.json'

declare module 'i18next' {
  interface CustomTypeOptions {
    defaultNS: 'translation'
    resources: { translation: typeof en }
    returnNull: false
  }
}
