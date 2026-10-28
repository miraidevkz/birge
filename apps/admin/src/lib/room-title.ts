// Names a group chat room for staff. The API gives the title as a locale key
// ("chat.room.district.<code>", "chat.room.topic.<code>"); the words come from
// the same `chat:roomTitle.*` and `reference:*` keys the mom app uses, so
// staff and members see the same room name. Mirrors apps/mobile/src/lib/reference.ts.
import { i18n } from '@birge/i18n';

function referenceLabel(group: 'district' | 'need', code: string): string {
  return i18n.t(`reference:${group}.${code}`, { defaultValue: code });
}

/** roomTitle returns the localized name of a group room, or the key itself when it has an unknown shape. */
export function roomTitle(titleKey: string): string {
  const district = /^chat\.room\.district\.([a-z_]+)$/.exec(titleKey);
  if (district?.[1]) return i18n.t('chat:roomTitle.district', { name: referenceLabel('district', district[1]) });
  const topic = /^chat\.room\.topic\.([a-z_]+)$/.exec(titleKey);
  if (topic?.[1]) return i18n.t('chat:roomTitle.topic', { name: referenceLabel('need', topic[1]) });
  return titleKey;
}
