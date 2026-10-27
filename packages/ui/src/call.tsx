import { Linking } from 'react-native';

import { Banner } from './banner';
import { Button } from './button';
import { ListItem } from './list-item';

/** CallItemProps: one phone line; all text comes from the screen's locale. */
export interface CallItemProps {
  label: string;
  number: string;
  /** accessibilityLabel says what tapping does, e.g. "Call <label>, <number>". */
  accessibilityLabel: string;
  testID?: string;
}

/**
 * CallItem is a list row that dials number on tap. Crisis and help screens
 * share it so every crisis line looks and behaves the same.
 */
export function CallItem({ label, number, accessibilityLabel, testID }: CallItemProps) {
  return (
    <ListItem
      title={label}
      subtitle={number}
      accessibilityLabel={accessibilityLabel}
      onPress={() => void Linking.openURL(`tel:${number}`)}
      testID={testID}
    />
  );
}

/** LifeThreatBannerProps: the message and the call button text from the locale. */
export interface LifeThreatBannerProps {
  message: string;
  callTitle: string;
  /** number is the emergency number to dial; 112 unless a deployment differs. */
  number?: string;
}

/**
 * LifeThreatBanner is the "life in danger: call 112" banner. It dials
 * directly and depends on nothing but the device (crisis path, ADR-0023).
 */
export function LifeThreatBanner({ message, callTitle, number = '112' }: LifeThreatBannerProps) {
  return (
    <Banner
      tone="danger"
      action={
        <Button
          title={callTitle}
          variant="destructive"
          onPress={() => void Linking.openURL(`tel:${number}`)}
          testID="call-112"
        />
      }
    >
      {message}
    </Banner>
  );
}
