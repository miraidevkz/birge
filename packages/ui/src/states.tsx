// The three non-content states every data screen has (loading, empty, error).
// They live here so each screen gets the same texts, accessibility and motion
// behaviour (ADR-0014) and never shows "No items" during the first load.
import { useTranslation } from '@birge/i18n';
import { ActivityIndicator, View } from 'react-native';

import { Button } from './button';
import { Logo } from './logo';
import { useMotionAllowed } from './motion';
import { Heading, Text } from './text';
import { useThemeColors } from './theme';

/** LoadingState shows progress while the first load runs. Without motion it shows only the text. */
export function LoadingState({ label }: { label?: string }) {
  const { t } = useTranslation('ui');
  const colors = useThemeColors();
  const motion = useMotionAllowed();
  const text = label ?? t('loading');
  return (
    <View
      className="items-center justify-center gap-3 py-8"
      accessible
      accessibilityLabel={text}
      accessibilityLiveRegion="polite"
      accessibilityState={{ busy: true }}
    >
      {motion ? <ActivityIndicator color={colors.primary} /> : null}
      <Text tone="muted">{text}</Text>
    </View>
  );
}

/** EmptyStateProps explain why there is nothing and, when useful, offer the next step. */
export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: { label: string; onPress: () => void };
}

/**
 * Mark is the soft decorative motif of empty states (ADR-0025): the brand's
 * circle of dots on a sand circle. Decorative only: hidden from screen
 * readers, and nothing about the app's subject.
 */
function Mark() {
  return (
    <View
      className="h-20 w-20 items-center justify-center rounded-full bg-accent"
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      <Logo size="sm" />
    </View>
  );
}

/** EmptyState is shown when data loaded and has no items. */
export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <View className="items-center gap-3 px-4 py-10">
      <Mark />
      <Heading className="text-center">{title}</Heading>
      {description ? (
        <Text tone="muted" className="text-center">
          {description}
        </Text>
      ) : null}
      {action ? <Button title={action.label} onPress={action.onPress} variant="secondary" /> : null}
    </View>
  );
}

/** ErrorStateProps: texts default to the generic connection message. */
export interface ErrorStateProps {
  title?: string;
  description?: string;
  onRetry?: () => void;
}

/** ErrorState is shown when the first load failed and there is nothing cached to show. */
export function ErrorState({ title, description, onRetry }: ErrorStateProps) {
  const { t } = useTranslation('ui');
  return (
    <View className="items-center gap-2 py-8" accessibilityLiveRegion="polite">
      <Heading className="text-center">{title ?? t('error.title')}</Heading>
      <Text tone="muted" className="text-center">
        {description ?? t('error.description')}
      </Text>
      {onRetry ? <Button title={t('error.retry')} onPress={onRetry} variant="secondary" /> : null}
    </View>
  );
}
