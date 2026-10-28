import type { ReactNode } from 'react';
import { ScrollView, View } from 'react-native';
import { KeyboardAwareScrollView } from 'react-native-keyboard-controller';
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
   * Ignored when layout is "form" (the form group always scrolls if needed).
   */
  scroll?: boolean;
  /**
   * variant "crisis" is for the help and SOS screens: no animation anywhere
   * inside, and no SOS entry (the screen is the destination).
   */
  variant?: 'default' | 'crisis';
  /**
   * layout "form" vertically centres the title, body and primary action as one
   * keyboard-aware group (sign-in and similar short forms). Default keeps the
   * title in the top bar and the footer pinned below the content.
   */
  layout?: 'default' | 'form';
  /** footer stays with the content: the screen's one primary action. */
  footer?: ReactNode;
  testID?: string;
}

/**
 * Screen gives every screen safe-area padding, the background, the title row
 * with the SOS entry, and optional scrolling and footer.
 */
export function Screen({
  children,
  title,
  scroll = true,
  variant = 'default',
  layout = 'default',
  footer,
  testID,
}: ScreenProps) {
  const insets = useSafeAreaInsets();
  const crisis = variant === 'crisis';
  const form = layout === 'form';
  const bottomPad = Math.max(insets.bottom, tokens.spacing[3]);

  const header = (
    <View
      className={cn(
        'min-h-touch flex-row items-center gap-3 px-5 pb-2 pt-3',
        title && !form ? 'justify-between' : 'justify-end',
      )}
    >
      {title && !form ? (
        <View className="flex-1">
          <Heading level={1}>{title}</Heading>
        </View>
      ) : null}
      {crisis ? null : <SosButton />}
    </View>
  );

  // KeyboardAwareScrollView keeps the focused field and the action above the
  // keyboard on both platforms. KeyboardAvoidingView did not: with
  // edge-to-edge (Expo SDK 58) Android no longer resizes the window, and the
  // phone and code fields stayed under the keyboard (Nokia 7.2).
  const formBody = (
    <KeyboardAwareScrollView
      className="flex-1"
      contentContainerClassName="flex-grow justify-center gap-5 px-5 py-6"
      keyboardShouldPersistTaps="handled"
      // Room under the focused field for its hint and the primary button.
      bottomOffset={tokens.touchTarget + tokens.spacing[16]}
    >
      {title ? <Heading level={1}>{title}</Heading> : null}
      {children}
      {footer ? <View className="gap-2 pt-2">{footer}</View> : null}
    </KeyboardAwareScrollView>
  );

  const defaultContent = scroll ? (
    <ScrollView className="flex-1" contentContainerClassName="gap-5 px-5 pb-8" keyboardShouldPersistTaps="handled">
      {children}
    </ScrollView>
  ) : (
    <View className="flex-1 gap-5 px-5">{children}</View>
  );

  const frame = (
    <View
      className="flex-1 bg-background"
      // Insets are device measurements, not design values.
      style={{ paddingTop: insets.top, paddingLeft: insets.left, paddingRight: insets.right }}
      testID={testID}
    >
      {/* One centred readable column on tablets and the web (ADR-0025). */}
      <View className="w-full max-w-content flex-1 self-center">
        {header}
        {form ? (
          formBody
        ) : (
          <>
            {defaultContent}
            {footer ? (
              <View className="gap-2 border-t border-separator px-5 pt-3" style={{ paddingBottom: bottomPad }}>
                {footer}
              </View>
            ) : null}
          </>
        )}
        {form ? <View style={{ height: bottomPad }} /> : null}
      </View>
    </View>
  );
  return crisis ? <NoMotion>{frame}</NoMotion> : frame;
}
