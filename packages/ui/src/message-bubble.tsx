import { Pressable, View } from 'react-native';

import { Avatar } from './avatar';
import { cn } from './cn';
import { Text } from './text';

/** MessageBubbleProps: one chat message as the reader sees it. */
export interface MessageBubbleProps {
  text: string;
  /** author is shown with an avatar for other people's messages. */
  author: string;
  mine: boolean;
  /** time is already formatted for the reader's language. */
  time?: string;
  /** muted shows a placeholder text (a hidden or held message) in the muted tone. */
  muted?: boolean;
  /** onPress makes the bubble a button (for example "report"); give accessibilityLabel with it. */
  onPress?: () => void;
  accessibilityLabel?: string;
  testID?: string;
}

/**
 * MessageBubble is a chat message (ADR-0025): her own on the right in the
 * primary colour, others on the left with an initials avatar and the name.
 */
export function MessageBubble({
  text,
  author,
  mine,
  time,
  muted,
  onPress,
  accessibilityLabel,
  testID,
}: MessageBubbleProps) {
  const bubble = (
    <View
      className={cn(
        'max-w-full gap-1 rounded-xl px-4 py-3',
        mine ? 'bg-primary' : 'bg-card border border-separator/60',
      )}
    >
      {mine ? null : (
        <Text variant="caption" tone="muted">
          {author}
        </Text>
      )}
      <Text tone={mine ? 'onPrimary' : muted ? 'muted' : 'default'}>{text}</Text>
      {time ? (
        <Text variant="caption" tone={mine ? 'onPrimary' : 'muted'} className="self-end">
          {time}
        </Text>
      ) : null}
    </View>
  );
  const content = onPress ? (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={accessibilityLabel}
      onPress={onPress}
      className="shrink active:opacity-80"
    >
      {bubble}
    </Pressable>
  ) : (
    <View className="shrink">{bubble}</View>
  );
  return (
    <View className={cn('flex-row items-end gap-2 py-1', mine ? 'justify-end pl-12' : 'pr-12')} testID={testID}>
      {mine ? null : <Avatar name={author} size="sm" />}
      {content}
    </View>
  );
}
