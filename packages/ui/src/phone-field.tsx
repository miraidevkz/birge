import { useRef, useState } from 'react';
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
 */
export function PhoneField({ value, onChangePhone, ...props }: PhoneFieldProps) {
  const display = formatPhone(value);
  const [selection, setSelection] = useState<{ start: number; end: number } | undefined>();
  // selectionRef is read in onChangeText; the parent value has not updated yet when that fires.
  const selectionRef = useRef({ start: 0, end: 0 });

  function onSelectionChange(e: TextInputSelectionChangeEvent) {
    const next = e.nativeEvent.selection;
    selectionRef.current = next;
    setSelection(next);
  }

  function onChangeText(text: string) {
    const result = applyPhoneEdit(display, text, selectionRef.current.start);
    const nextSelection = { start: result.caret, end: result.caret };
    selectionRef.current = nextSelection;
    setSelection(nextSelection);
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
