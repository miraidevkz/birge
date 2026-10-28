import { Image } from 'react-native';

import { tokens } from './theme';

/** PhotoProps: the image address and what it shows, for screen readers. */
export interface PhotoProps {
  /** uri is an http(s) address from the API; anything else is not loaded. */
  uri: string;
  /** accessibilityLabel says what the photo is (for example "Listing photo"), never who is in it. */
  accessibilityLabel: string;
  testID?: string;
}

// Only web addresses are loaded: a data:, file: or javascript: value from an
// untrusted source must not reach the image loader.
const allowedScheme = /^https?:\/\//i;

/**
 * Photo shows a content photo under review (a listing or specialist photo in
 * the staff console) at full width in a fixed 4:3 frame with the card's
 * rounded corners. Member avatars never use it: those stay initials (Avatar),
 * on purpose. A uri that is not http(s) renders nothing.
 */
export function Photo({ uri, accessibilityLabel, testID }: PhotoProps) {
  if (!allowedScheme.test(uri)) return null;
  return (
    <Image
      source={{ uri }}
      accessible
      accessibilityRole="image"
      accessibilityLabel={accessibilityLabel}
      resizeMode="contain"
      testID={testID}
      className="w-full bg-muted"
      style={{ aspectRatio: 4 / 3, borderRadius: tokens.radius.md }}
    />
  );
}
