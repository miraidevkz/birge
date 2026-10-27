import type { ReactNode } from 'react';
import { ScrollView, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { cn } from './cn';
import { NoMotion } from './motion';
import { SosButton } from './sos';
import { Heading } from './text';
import { tokens } from './theme';

/** ScreenProps: the frame of every screen. */
export interface ScreenProps {
  children: ReactNode;
  /** title is the screen's level-1 heading. */
  title?: string;
  /**
   * scroll wraps the content in a ScrollView. Turn it off when the content is
   * a List, which scrolls itself (nesting virtualized lists breaks them).
   */
  scroll?: boolean;
  /**
   * variant "crisis" is for the help and SOS screens: no animation anywhere
   * inside, and no SOS entry (the screen is the destination).
   */
  variant?: 'default' | 'crisis';
  /** footer stays below the content, above the home indicator: the screen's one primary action. */
  footer?: ReactNode;
  testID?: string;
}

/**
 * Screen gives every screen safe-area padding, the background, the title row
 * with the SOS entry, and optional scrolling and footer.
 */
export function Screen({ children, title, scroll = true, variant = 'default', footer, testID }: ScreenProps) {
  const insets = useSafeAreaInsets();
  const crisis = variant === 'crisis';
  const content = scroll ? (
    <ScrollView className="flex-1" contentContainerClassName="gap-4 px-4 pb-6" keyboardShouldPersistTaps="handled">
      {children}
    </ScrollView>
  ) : (
    <View className="flex-1 gap-4 px-4">{children}</View>
  );
  const frame = (
    <View
      className="flex-1 bg-background"
      // Insets are device measurements, not design values.
      style={{ paddingTop: insets.top, paddingLeft: insets.left, paddingRight: insets.right }}
      testID={testID}
    >
      <View
        className={cn('min-h-touch flex-row items-center gap-3 px-4 py-2', title ? 'justify-between' : 'justify-end')}
      >
        {title ? (
          <View className="flex-1">
            <Heading level={1}>{title}</Heading>
          </View>
        ) : null}
        {crisis ? null : <SosButton />}
      </View>
      {content}
      {footer ? (
        <View
          className="gap-2 border-t border-separator px-4 pt-3"
          style={{ paddingBottom: Math.max(insets.bottom, tokens.spacing[3]) }}
        >
          {footer}
        </View>
      ) : null}
    </View>
  );
  return crisis ? <NoMotion>{frame}</NoMotion> : frame;
}
