import { Button } from './button';
import { Sheet } from './sheet';
import { Text } from './text';

/** ConfirmSheetProps: what will happen, the button that does it, and how to back out. */
export interface ConfirmSheetProps {
  visible: boolean;
  title: string;
  /** message states the consequence plainly ("She will be suspended until an admin lifts it."). */
  message: string;
  /** confirmLabel names the action itself ("Lift suspension"), not "OK". */
  confirmLabel: string;
  onConfirm: () => void;
  onClose: () => void;
  /** destructive marks an action that is hard to undo (suspend, remove). */
  destructive?: boolean;
  loading?: boolean;
  testID?: string;
}

/**
 * ConfirmSheet asks once before an action with consequences for someone
 * else (suspending, lifting a suspension, a legal hold). It is a Sheet with
 * the consequence and one action button; the Sheet's own close button and
 * the backdrop back out, so there is no second "Cancel" to read. The confirm
 * button carries `${testID}-confirm`.
 */
export function ConfirmSheet({
  visible,
  title,
  message,
  confirmLabel,
  onConfirm,
  onClose,
  destructive = false,
  loading = false,
  testID,
}: ConfirmSheetProps) {
  return (
    <Sheet visible={visible} onClose={onClose} title={title} testID={testID}>
      <Text>{message}</Text>
      <Button
        title={confirmLabel}
        variant={destructive ? 'destructive' : 'primary'}
        onPress={onConfirm}
        loading={loading}
        testID={testID ? `${testID}-confirm` : undefined}
      />
    </Sheet>
  );
}
