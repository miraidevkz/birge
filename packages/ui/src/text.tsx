import { Text as RNText, type TextProps as RNTextProps } from 'react-native';

import { cn } from './cn';

const variants = {
  body: 'font-sans text-body',
  caption: 'font-sans text-caption',
  label: 'font-sans-semibold text-label',
  title: 'font-sans-bold text-title',
  display: 'font-sans-bold text-display',
} as const;

const tones = {
  default: 'text-foreground',
  muted: 'text-muted-foreground',
  primary: 'text-primary',
  destructive: 'text-destructive',
  onPrimary: 'text-primary-foreground',
  onSecondary: 'text-secondary-foreground',
  onDestructive: 'text-destructive-foreground',
  info: 'text-info-fg',
  success: 'text-success-fg',
  warning: 'text-warning-fg',
  danger: 'text-danger-fg',
} as const;

/** TextVariant is a step of the type ramp. */
export type TextVariant = keyof typeof variants;
/** TextTone is the semantic text color; pick the "on*" tone on a filled surface. */
export type TextTone = keyof typeof tones;

/** TextProps are React Native Text props plus variant and tone; `style` is not accepted (tokens only). */
export type TextProps = Omit<RNTextProps, 'style'> & {
  variant?: TextVariant;
  tone?: TextTone;
  className?: string;
};

/**
 * Text is the only way screens show text. It follows the system font size
 * (font scaling is never disabled) and wraps instead of truncating, so large
 * accessibility sizes stay readable.
 */
export function Text({ variant = 'body', tone = 'default', className, ...props }: TextProps) {
  return <RNText className={cn(variants[variant], tones[tone], className)} {...props} />;
}

/** HeadingProps adds the heading level. */
export type HeadingProps = Omit<TextProps, 'variant' | 'accessibilityRole'> & { level?: 1 | 2 };

/**
 * Heading is a section or screen title. It is announced as a header, so
 * screen-reader users can jump between sections.
 */
export function Heading({ level = 2, ...props }: HeadingProps) {
  return <Text accessibilityRole="header" variant={level === 1 ? 'display' : 'title'} {...props} />;
}
