import { describe, expect, it } from '@jest/globals';
import { readdirSync } from 'node:fs';
import { join } from 'node:path';

import { fallbackLanguage, languages, namespaces, pickLanguage, resources } from './index';

// Flattens a locale file to "a.b.c" -> value.
function flatten(obj: object, prefix = ''): Map<string, unknown> {
  const out = new Map<string, unknown>();
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === 'object' && !Array.isArray(v)) {
      for (const [ik, iv] of flatten(v as object, key)) out.set(ik, iv);
    } else {
      out.set(key, v);
    }
  }
  return out;
}

function placeholders(text: string): string[] {
  return [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1] ?? '').sort();
}

describe('locale files', () => {
  it('every language has exactly the registered namespace files', () => {
    for (const lang of languages) {
      const files = readdirSync(join(__dirname, 'locales', lang))
        .filter((f) => f.endsWith('.json'))
        .map((f) => f.replace(/\.json$/, ''))
        .sort();
      expect(files).toEqual([...namespaces].sort());
    }
  });

  for (const ns of namespaces) {
    it(`"${ns}" has the same keys, non-empty strings and the same placeholders in kk, ru and en`, () => {
      const reference = flatten(resources[fallbackLanguage][ns]);
      for (const lang of languages) {
        const current = flatten(resources[lang][ns]);
        expect([...current.keys()].sort()).toEqual([...reference.keys()].sort());
        for (const [key, value] of current) {
          expect({ lang, ns, key, ok: typeof value === 'string' && value.trim().length > 0 }).toEqual({
            lang,
            ns,
            key,
            ok: true,
          });
          expect({ lang, ns, key, p: placeholders(value as string) }).toEqual({
            lang,
            ns,
            key,
            p: placeholders(reference.get(key) as string),
          });
        }
      }
    });
  }
});

describe('pickLanguage', () => {
  it('takes the first supported device language by primary subtag', () => {
    expect(pickLanguage(['de-DE', 'ru-KZ', 'en'])).toBe('ru');
    expect(pickLanguage(['en_US'])).toBe('en');
    expect(pickLanguage(['KK'])).toBe('kk');
  });

  it('falls back to Kazakh', () => {
    expect(pickLanguage([])).toBe('kk');
    expect(pickLanguage(['de', 'fr'])).toBe('kk');
  });
});
