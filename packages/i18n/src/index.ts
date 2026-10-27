// @birge/i18n owns every user-visible string of the clients (AGENTS.md: UI
// strings live only in locale files). Components call useTranslation(ns) from
// this package; nothing else imports i18next directly.
//
// Language choice (ADR-0014): the first device language we support, otherwise
// Kazakh. A missing key in the chosen language falls back to Kazakh as well,
// so a new language can ship before every text is translated.
import { createInstance, type i18n as I18n } from 'i18next';
import { initReactI18next, useTranslation } from 'react-i18next';

import { namespaces, resources, type Namespace } from './resources';

export { namespaces, resources, useTranslation };
export type { Namespace };

/** languages are the interface languages at launch, in the order shown to users. */
export const languages = ['kk', 'ru', 'en'] as const;

/** Language is one supported interface language (matches the API's Lang). */
export type Language = (typeof languages)[number];

/** fallbackLanguage is used when no device language is supported. */
export const fallbackLanguage: Language = 'kk';

// One instance for the whole app; initReactI18next makes it the one
// useTranslation reads.
const instance: I18n = createInstance();

/**
 * pickLanguage returns the first supported language among BCP 47 tags in the
 * device's preference order ("ru-KZ" counts as "ru"), or the fallback.
 */
export function pickLanguage(deviceTags: readonly string[]): Language {
  for (const tag of deviceTags) {
    const primary = tag.toLowerCase().split(/[-_]/)[0];
    const match = languages.find((l) => l === primary);
    if (match) return match;
  }
  return fallbackLanguage;
}

/**
 * initI18n initialises the shared i18next instance synchronously and returns
 * it. Resources are bundled, so there is nothing to load and the first render
 * already has its texts. Calling it again only changes the language.
 */
export function initI18n(deviceTags: readonly string[]): I18n {
  const lng = pickLanguage(deviceTags);
  if (instance.isInitialized) {
    void instance.changeLanguage(lng);
    return instance;
  }
  void instance.use(initReactI18next).init({
    resources,
    lng,
    fallbackLng: fallbackLanguage,
    supportedLngs: [...languages],
    ns: [...namespaces],
    defaultNS: 'common',
    initAsync: false,
    // React escapes output already; double escaping would show "&amp;".
    interpolation: { escapeValue: false },
    returnNull: false,
    returnEmptyString: false,
  });
  return instance;
}

/** i18n is the shared instance, for code outside React (for example formatting). */
export const i18n = instance;
