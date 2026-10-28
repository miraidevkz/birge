import { View } from 'react-native';

import { cn } from './cn';
import { Text } from './text';

/** LOGO_SIZES are the mark's sizes in px; the wordmark scales with them. */
const sizes = { sm: 'h-12 w-12', md: 'h-16 w-16', lg: 'h-20 w-20' } as const;

// The six dots of the "Shenber" mark (branding/render.py: ring 124, dot 40 on
// a 512 grid), as percentages of the box so any size keeps the same shape.
const DOTS = [0, 1, 2, 3, 4, 5].map((k) => {
  const a = ((-90 + 60 * k) * Math.PI) / 180;
  const c = 50 + (124 / 512) * 100 * Math.cos(a);
  const m = 50 + (124 / 512) * 100 * Math.sin(a);
  const d = (80 / 512) * 100;
  return { left: `${c - d / 2}%`, top: `${m - d / 2}%`, width: `${d}%`, height: `${d}%` } as const;
});

/** LogoProps: size of the mark; withName adds the wordmark from the caller's locale. */
export interface LogoProps {
  size?: keyof typeof sizes;
  /** name is the localized product name shown beside the mark, e.g. "Бірге". */
  name?: string;
}

/**
 * Logo is the brand mark (six dots in a circle, docs/adr/0025 and
 * branding/README.md) drawn from views in the primary colour, so it follows
 * the theme and dark mode without image files. Decorative: the name, when
 * shown, is the accessible text.
 */
export function Logo({ size = 'md', name }: LogoProps) {
  return (
    <View className="flex-row items-center gap-3">
      <View
        className={cn('relative', sizes[size])}
        accessibilityElementsHidden
        importantForAccessibility="no-hide-descendants"
      >
        {DOTS.map((style, i) => (
          // Positions are geometry derived from the mark, not design tokens.
          <View key={i} className="absolute rounded-full bg-primary" style={style} />
        ))}
      </View>
      {name ? (
        <Text variant="display" tone="primary" accessibilityRole="header">
          {name}
        </Text>
      ) : null}
    </View>
  );
}
