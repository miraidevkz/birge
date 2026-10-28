import { clsx, type ClassValue } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';

import { tokens } from './theme';

/**
 * twMerge taught our type ramp. Out of the box it does not know `text-body`,
 * `text-title` or `text-display`, takes them for text colours and drops them
 * next to `text-foreground`: every Text then rendered at the platform default
 * size, the "same small font everywhere" look (docs/solved-problems.md).
 */
const twMerge = extendTailwindMerge({
  extend: { classGroups: { 'font-size': [{ text: Object.keys(tokens.fontSize) }] } },
});

/**
 * cn joins class names and lets later ones win over earlier conflicting ones
 * (RN Reusables convention), so a caller's `className` can adjust layout
 * without fighting the component's defaults.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
