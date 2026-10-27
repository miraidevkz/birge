import { useState } from 'react';
import type { TextInputSelectionChangeEvent } from 'react-native';

import { applyPhoneEdit, formatPhone } from './phone';
import { TextField, type TextFieldProps } from './text-field';

/** PhoneFieldProps: TextField without raw text handlers; value is E.164-shaped. */
export type PhoneFieldProps = Omit<
  TextFieldProps,
  | 'value'
  | 'onChangeText'
  | 'onChange'
  | 'keyboardType'
  | 'textContentType'
  | 'autoComplete'
  | 'selection'
  | 'onSelectionChange'
> & {
  /** value is '+' and digits (what the API and Kratos need), never the formatted display. */
  value: string;
  /** onChangePhone reports the E.164-shaped value after each edit. */
  onChangePhone: (e164: string) => void;
};

/**
 * PhoneField is a labelled phone input that formats as the user types.
 * Screens keep only the E.164-shaped value; formatting and caret rules live in phone.ts.
 * Controlled `selection` places the caret where applyPhoneEdit decided;
 * onSelectionChange only keeps that state in sync with taps — it does not feed caret math
 * (RN does not guarantee onSelectionChange vs onChangeText order).
 */
export function PhoneField({ value, onChangePhone, ...props }: PhoneFieldProps) {
  const display = formatPhone(value);
  const [selection, setSelection] = useState<{ start: number; end: number } | undefined>();

  function onSelectionChange(e: TextInputSelectionChangeEvent) {
    setSelection(e.nativeEvent.selection);
  }

  function onChangeText(text: string) {
    const result = applyPhoneEdit(display, text);
    setSelection({ start: result.caret, end: result.caret });
    onChangePhone(result.e164);
  }

  return (
    <TextField
      {...props}
      value={display}
      onChangeText={onChangeText}
      onSelectionChange={onSelectionChange}
      selection={selection}
      keyboardType="phone-pad"
      textContentType="telephoneNumber"
      autoComplete="tel"
    />
  );
}
