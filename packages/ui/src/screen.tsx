import type { ReactNode } from 'react';
import { ScrollView, View } from 'react-native';
import { KeyboardAwareScrollView } from 'react-native-keyboard-controller';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { Appear } from './appear';
import { useScreenBack } from './back';
import { Button } from './button';
import { cn } from './cn';
import { NoMotion } from './motion';
import { SosButton } from './sos';
import { Heading } from './text';
import { tokens } from './theme';
import { useTranslation } from '@birge/i18n';

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
  /**
   * transitionKey names the content that is showing (a step of a flow). When
   * it changes, the new content fades in; the title, SOS entry and footer stay.
   * Static under reduce motion and on crisis screens.
   */
  transitionKey?: string;
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
  transitionKey,
  testID,
}: ScreenProps) {
  const insets = useSafeAreaInsets();
  const crisis = variant === 'crisis';
  const form = layout === 'form';
  const bottomPad = Math.max(insets.bottom, tokens.spacing[3]);
  const onBack = useScreenBack();
  const { t } = useTranslation('ui');
  // The key remounts the wrapper, so the new step plays its fade once; the
  // wrapper repeats the parent's gap-5 because the content is no longer the
  // parent's direct children. The old step leaves at once: a fade-out would
  // stack both steps in the scroll view for its duration.
  const content =
    transitionKey === undefined ? (
      children
    ) : (
      <Appear key={transitionKey} rise={false} className="gap-5">
        {children}
      </Appear>
    );

  const header = (
    <View
      className={cn(
        'min-h-touch flex-row items-center gap-3 px-5 pb-2 pt-3',
        title && !form ? 'justify-between' : 'justify-end',
      )}
    >
      {onBack ? <Button title={t('back')} variant="secondary" size="sm" onPress={onBack} testID="screen-back" /> : null}
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
      // Style, not contentContainerClassName: NativeWind does not map class
      // names onto this third-party component on the web, and the form lost
      // its gutters and gaps there.
      contentContainerStyle={{
        flexGrow: 1,
        justifyContent: 'center',
        gap: tokens.spacing[5],
        paddingHorizontal: tokens.spacing[5],
        paddingVertical: tokens.spacing[6],
      }}
      keyboardShouldPersistTaps="handled"
      // Room under the focused field for its hint and the primary button.
      bottomOffset={tokens.touchTarget + tokens.spacing[16]}
    >
      {title ? <Heading level={1}>{title}</Heading> : null}
      {content}
      {footer ? <View className="gap-2 pt-2">{footer}</View> : null}
    </KeyboardAwareScrollView>
  );

  const defaultContent = scroll ? (
    <ScrollView className="flex-1" contentContainerClassName="gap-5 px-5 pb-8" keyboardShouldPersistTaps="handled">
      {content}
    </ScrollView>
  ) : (
    <View className="flex-1 gap-5 px-5">{content}</View>
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
