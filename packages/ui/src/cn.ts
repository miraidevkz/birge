import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * cn joins class names and lets later ones win over earlier conflicting ones
 * (RN Reusables convention), so a caller's `className` can adjust layout
 * without fighting the component's defaults.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
