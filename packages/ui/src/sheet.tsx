import { useTranslation } from '@birge/i18n';
import type { ReactNode } from 'react';
import { Modal, Pressable, StyleSheet, View } from 'react-native';
import Animated, { FadeIn, SlideInDown } from 'react-native-reanimated';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { Button } from './button';
import { useDuration } from './motion';
import { Heading } from './text';
import { tokens } from './theme';

/** SheetProps: a titled panel over the screen, closed by its button, the backdrop or the back gesture. */
export interface SheetProps {
  visible: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  testID?: string;
}

/**
 * Sheet is a bottom panel for a short, related task (pick an option, confirm
 * details). For a task that deserves its own route, prefer a router
 * formSheet. The panel slides in on the UI thread within the token duration;
 * with reduce motion or on crisis screens it appears without motion.
 */
export function Sheet({ visible, onClose, title, children, testID }: SheetProps) {
  const { t } = useTranslation('ui');
  const insets = useSafeAreaInsets();
  const duration = useDuration('base');
  return (
    <Modal visible={visible} transparent animationType="none" onRequestClose={onClose} statusBarTranslucent>
      <View className="flex-1 justify-end">
        {/* Reanimated views carry only the motion; NativeWind classes sit on
            plain views inside them. */}
        <Animated.View entering={duration ? FadeIn.duration(duration) : undefined} style={StyleSheet.absoluteFill}>
          <Pressable
            className="flex-1 bg-foreground/40"
            accessibilityRole="button"
            accessibilityLabel={t('sheet.close')}
            onPress={onClose}
          />
        </Animated.View>
        <Animated.View entering={duration ? SlideInDown.duration(duration) : undefined}>
          <View
            accessibilityViewIsModal
            className="gap-4 rounded-t-lg bg-card px-4 pt-4"
            style={{ paddingBottom: Math.max(insets.bottom, tokens.spacing[4]) }}
            testID={testID}
          >
            <Heading>{title}</Heading>
            {children}
            <Button title={t('sheet.close')} onPress={onClose} variant="secondary" />
          </View>
        </Animated.View>
      </View>
    </Modal>
  );
}
