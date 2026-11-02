// SPDX-License-Identifier: AGPL-3.0-or-later
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import type { Language } from '../api/types'
import { readStorage, writeStorage } from '../lib/storage'
import en from './en.json'

export const LANGUAGES = ['en', 'fr'] as const satisfies readonly Language[]
export const DEFAULT_LANGUAGE: Language = 'en'
const STORAGE_KEY = 'cwnsb.lang'

// Compile-time parity check: each catalog must have every key of the other. French is only a
// type here: its catalog is a separate chunk, fetched when French is used (English, the
// fallback, is bundled).
type Fr = typeof import('./fr.json')
type Same<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false
export type CatalogParity = Same<typeof en, Fr> extends true ? true : never
const parity: CatalogParity = true

const LOADERS: Record<Exclude<Language, 'en'>, () => Promise<{ default: Fr }>> = {
  fr: () => import('./fr.json'),
}

/** Fetches a language's catalog once (English is always there). */
async function ensureCatalog(lang: Language): Promise<void> {
  if (lang === 'en' || i18n.hasResourceBundle(lang, 'translation')) return
  const catalog = await LOADERS[lang]()
  i18n.addResourceBundle(lang, 'translation', catalog.default)
}

export function isLanguage(value: unknown): value is Language {
  return typeof value === 'string' && (LANGUAGES as readonly string[]).includes(value)
}

/**
 * Language before login: saved choice > browser language (fr* → fr) > English.
 * Once logged in, the account's language wins (applied by the session).
 */
export function detectLanguage(): Language {
  const saved = readStorage(STORAGE_KEY)
  if (isLanguage(saved)) return saved
  const browser = typeof navigator !== 'undefined' ? navigator.language : ''
  return browser.toLowerCase().startsWith('fr') ? 'fr' : DEFAULT_LANGUAGE
}

/** Switches the UI language (fetching its catalog first) and remembers it in this browser. */
export async function applyLanguage(lang: Language): Promise<void> {
  writeStorage(STORAGE_KEY, lang)
  await ensureCatalog(lang)
  if (i18n.language !== lang) await i18n.changeLanguage(lang)
}

export function currentLanguage(): Language {
  const lang = i18n.resolvedLanguage ?? i18n.language
  return isLanguage(lang) ? lang : DEFAULT_LANGUAGE
}

i18n.on('languageChanged', (lang) => {
  document.documentElement.lang = lang
})

const initial = detectLanguage()

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en } },
  partialBundledLanguages: true,
  lng: 'en',
  fallbackLng: DEFAULT_LANGUAGE,
  supportedLngs: [...LANGUAGES],
  initAsync: false, // English is bundled: no need to wait
  interpolation: { escapeValue: false }, // React already escapes
  returnNull: false,
  react: { useSuspense: false },
})

/**
 * Resolves once the first language is ready (render after it: no flash of English). A catalog
 * that fails to load leaves the UI in English.
 */
export const i18nReady: Promise<void> = (initial === 'en' ? Promise.resolve() : applyLanguage(initial))
  .catch(() => undefined)
  .then(() => {
    document.documentElement.lang = currentLanguage()
    void parity
  })

export default i18n
