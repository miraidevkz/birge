import type { ReactNode } from 'react';
import { View } from 'react-native';

import { cn } from './cn';
import { Text } from './text';

const tones = {
  info: { box: 'bg-info-bg', text: 'info' },
  success: { box: 'bg-success-bg', text: 'success' },
  warning: { box: 'bg-warning-bg', text: 'warning' },
  danger: { box: 'bg-danger-bg', text: 'danger' },
} as const;

/** BannerTone is the meaning of the message. */
export type BannerTone = keyof typeof tones;

/** BannerProps: a short message, an optional title and an optional action below it. */
export interface BannerProps {
  tone?: BannerTone;
  title?: string;
  children: string;
  action?: ReactNode;
  testID?: string;
}

/**
 * Banner is an inline status message that stays on screen (not a toast that
 * disappears before a tired reader finishes it). Warning and danger banners
 * are announced as alerts; info and success politely.
 */
export function Banner({ tone = 'info', title, children, action, testID }: BannerProps) {
  const t = tones[tone];
  const urgent = tone === 'warning' || tone === 'danger';
  return (
    <View
      className={cn('gap-2 rounded-md p-4', t.box)}
      accessibilityRole={urgent ? 'alert' : 'summary'}
      accessibilityLiveRegion={urgent ? 'assertive' : 'polite'}
      testID={testID}
    >
      {title ? (
        <Text variant="label" tone={t.text}>
          {title}
        </Text>
      ) : null}
      <Text tone={t.text}>{children}</Text>
      {action}
    </View>
  );
}
