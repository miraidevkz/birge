import { describe, expect, it } from '@jest/globals';

import { cn } from './cn';
import { tokens, type ColorName } from './theme';

// WCAG 2.2 relative luminance and contrast ratio.
function luminance(hex: string): number {
  const n = parseInt(hex.slice(1), 16);
  const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
}

// Every text-on-surface combination the components use (AA: 4.5:1).
const textPairs: [ColorName, ColorName][] = [
  ['foreground', 'background'],
  ['foreground', 'card'],
  ['mutedForeground', 'background'],
  ['mutedForeground', 'card'],
  ['mutedForeground', 'muted'],
  ['cardForeground', 'card'],
  ['primary', 'background'],
  ['primary', 'card'],
  ['destructive', 'background'],
  ['destructive', 'card'],
  ['primaryForeground', 'primary'],
  ['secondaryForeground', 'secondary'],
  ['destructiveForeground', 'destructive'],
  ['infoFg', 'infoBg'],
  ['successFg', 'successBg'],
  ['warningFg', 'warningBg'],
  ['dangerFg', 'dangerBg'],
  ['accentForeground', 'accent'],
];

// Input outlines against their surfaces (non-text contrast: 3:1).
const uiPairs: [ColorName, ColorName][] = [
  ['border', 'background'],
  ['border', 'card'],
  ['destructive', 'card'],
];

describe('color tokens', () => {
  for (const scheme of ['light', 'dark'] as const) {
    const c = tokens.colors[scheme];
    it(`${scheme}: text pairs meet WCAG AA 4.5:1`, () => {
      for (const [fg, bg] of textPairs) {
        expect({ fg, bg, ok: contrast(c[fg], c[bg]) >= 4.5 }).toEqual({ fg, bg, ok: true });
      }
    });
    it(`${scheme}: input outlines meet 3:1`, () => {
      for (const [fg, bg] of uiPairs) {
        expect({ fg, bg, ok: contrast(c[fg], c[bg]) >= 3 }).toEqual({ fg, bg, ok: true });
      }
    });
  }

  it('light and dark define the same tokens', () => {
    expect(Object.keys(tokens.colors.dark).sort()).toEqual(Object.keys(tokens.colors.light).sort());
  });
});

describe('motion tokens', () => {
  it('stay within 150-250 ms (ADR-0014)', () => {
    for (const ms of Object.values(tokens.motion)) {
      expect(ms).toBeGreaterThanOrEqual(150);
      expect(ms).toBeLessThanOrEqual(250);
    }
  });
});

describe('cn', () => {
  it('keeps a type-ramp size next to a text colour', () => {
    for (const size of ['caption', 'body', 'label', 'title', 'display']) {
      expect(cn(`text-${size}`, 'text-foreground').split(' ')).toEqual([`text-${size}`, 'text-foreground']);
    }
    // A later size still wins over an earlier one.
    expect(cn('text-body', 'text-title')).toBe('text-title');
  });
});
