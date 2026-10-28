// Reference data (cities with districts, needs codes) and their labels. The
// API returns codes; labels live in the `reference` locale namespace, and a
// code without a label is shown as is, so new reference data never breaks
// the screen.
import { unwrap } from '@birge/api';
import { i18n } from '@birge/i18n';
import { useQuery } from '@tanstack/react-query';

import { api } from './api';

function label(group: 'city' | 'district' | 'need' | 'profession' | 'category', code: string): string {
  return i18n.t(`reference:${group}.${code}`, { defaultValue: code });
}

/** cityLabel, districtLabel, needLabel, professionLabel and categoryLabel name a reference code in the current language. */
export const cityLabel = (code: string) => label('city', code);
export const districtLabel = (code: string) => label('district', code);
export const needLabel = (code: string) => label('need', code);
export const professionLabel = (code: string) => label('profession', code);
export const categoryLabel = (code: string) => label('category', code);

/** useDistricts loads the cities and their districts; reference data changes rarely. */
export function useDistricts() {
  return useQuery({
    queryKey: ['reference', 'districts'],
    queryFn: () => unwrap(api.GET('/reference/districts')),
    staleTime: 60 * 60 * 1000,
  });
}

/** useNeeds loads the needs codes. */
export function useNeeds() {
  return useQuery({
    queryKey: ['reference', 'needs'],
    queryFn: () => unwrap(api.GET('/reference/needs')),
    staleTime: 60 * 60 * 1000,
  });
}

/**
 * roomTitle names a chat room. Group rooms carry a locale key from the API
 * ("chat.room.district.<code>", "chat.room.topic.<code>"); direct rooms carry
 * the other mother's first name, shown as is.
 */
export function roomTitle(title: string): string {
  const district = /^chat\.room\.district\.([a-z_]+)$/.exec(title);
  if (district?.[1]) return i18n.t('chat:roomTitle.district', { name: districtLabel(district[1]) });
  const topic = /^chat\.room\.topic\.([a-z_]+)$/.exec(title);
  if (topic?.[1]) return i18n.t('chat:roomTitle.topic', { name: needLabel(topic[1]) });
  return title;
}
