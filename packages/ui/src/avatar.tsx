import { View } from 'react-native';

import { cn } from './cn';
import { Text } from './text';

// Soft token surfaces with their text colour; every pair passes AA (theme test).
const tints = [
  { box: 'bg-accent', text: 'text-accent-foreground' },
  { box: 'bg-secondary', text: 'text-secondary-foreground' },
  { box: 'bg-info-bg', text: 'text-info-fg' },
  { box: 'bg-success-bg', text: 'text-success-fg' },
] as const;

const sizes = { sm: 'h-10 w-10', md: 'h-12 w-12' } as const;

/** initialsOf returns up to two initials of a display name ("Айгерим Сапарова" -> "АС"). */
export function initialsOf(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  const letters = words.slice(0, 2).map((w) => Array.from(w)[0] ?? '');
  return letters.join('').toLocaleUpperCase() || '·';
}

/** tintOf picks a stable tint for a name, so the same person keeps her colour. */
export function tintOf(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + (ch.codePointAt(0) ?? 0)) >>> 0;
  return h % tints.length;
}

/** AvatarProps: the person's display name (first name only, as the API gives it). */
export interface AvatarProps {
  name: string;
  size?: keyof typeof sizes;
}

/**
 * Avatar is a person's initials in a soft tinted circle (ADR-0025). There are
 * no profile photos: a face would make a member recognisable to anyone
 * looking over a shoulder. Decorative: the name next to it is the label.
 */
export function Avatar({ name, size = 'md' }: AvatarProps) {
  const tint = tints[tintOf(name)] ?? tints[0];
  return (
    <View
      className={cn('items-center justify-center rounded-full', sizes[size], tint.box)}
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      <Text variant="label" className={tint.text}>
        {initialsOf(name)}
      </Text>
    </View>
  );
}
