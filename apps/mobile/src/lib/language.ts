// The member's interface language. Her explicit choice wins over the device
// language and over the server: it is kept on the device, applied at start,
// and sent to the server after sign-in (the server uses it for notification
// texts). Before this, the profile pulled the server's default (kk) and
// switched the app's language on its own.
import { i18n, languages, type Language } from '@birge/i18n';

import { storage } from './storage';

/** LANGUAGE_KEY is the storage key of the chosen language. */
export const LANGUAGE_KEY = 'birge.lang';

function isLanguage(v: unknown): v is Language {
  return typeof v === 'string' && (languages as readonly string[]).includes(v);
}

/** currentLanguage is the language the interface shows now. */
export function currentLanguage(): Language {
  return isLanguage(i18n.language) ? i18n.language : 'kk';
}

/** loadSavedLanguage applies a choice saved earlier; without one the device language stays. */
export async function loadSavedLanguage(): Promise<void> {
  try {
    const saved = await storage.get(LANGUAGE_KEY);
    if (isLanguage(saved) && saved !== i18n.language) await i18n.changeLanguage(saved);
  } catch {
    // Unreadable storage: keep the device language.
  }
}

/** chooseLanguage switches the interface and remembers the choice on this device. */
export async function chooseLanguage(lang: Language): Promise<void> {
  await i18n.changeLanguage(lang);
  try {
    await storage.set(LANGUAGE_KEY, lang);
  } catch {
    // Not remembered; the switch itself still applies for this run.
  }
}
