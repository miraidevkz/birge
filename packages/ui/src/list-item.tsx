import { memo, type ReactNode } from 'react';
import { Pressable, View } from 'react-native';

import { cn } from './cn';
import { Text } from './text';

/** ListItemProps: a title, optional subtitle, leading (avatar) and trailing element (badge, chevron). */
export interface ListItemProps {
  /** leading sits before the text, for example an Avatar. */
  leading?: ReactNode;
  title: string;
  subtitle?: string;
  trailing?: ReactNode;
  onPress?: () => void;
  /** emphasis shows the title in the strong weight, e.g. for unread items. */
  emphasis?: boolean;
  /** accessibilityLabel overrides the default "title, subtitle". */
  accessibilityLabel?: string;
  testID?: string;
}

/**
 * ListItem is one row of a List: at least one touch target high, text wraps
 * at large font sizes. Memoized because lists re-render rows often.
 */
export const ListItem = memo(function ListItem({
  title,
  subtitle,
  leading,
  trailing,
  onPress,
  emphasis = false,
  accessibilityLabel,
  testID,
}: ListItemProps) {
  const label = accessibilityLabel ?? (subtitle ? `${title}, ${subtitle}` : title);
  const body = (
    <>
      {leading}
      <View className="flex-1 gap-1">
        <Text variant={emphasis ? 'label' : 'body'}>{title}</Text>
        {subtitle ? (
          <Text variant="caption" tone="muted">
            {subtitle}
          </Text>
        ) : null}
      </View>
      {trailing}
    </>
  );
  const row = 'min-h-touch flex-row items-center gap-3 border-b border-separator py-3';
  if (onPress) {
    return (
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={label}
        onPress={onPress}
        testID={testID}
        className={cn(row, 'active:opacity-80')}
      >
        {body}
      </Pressable>
    );
  }
  return (
    <View accessible accessibilityLabel={label} className={row} testID={testID}>
      {body}
    </View>
  );
});
