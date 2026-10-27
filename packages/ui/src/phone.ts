/**
 * Phone number parse/format for PhoneField.
 *
 * Value contract is E.164-shaped: a leading '+' and digits only. Kazakhstan
 * (+7) is shown as `+7 (XXX) XXX-XX-XX`; a leading 8 or 7 without '+' becomes
 * +7. Other country codes keep '+' and group digits by threes. No masking
 * library (ADR-0012): caret math and separator-backspace live here so screens
 * never re-implement formatting.
 */

/** PHONE_MAX_DIGITS is the E.164 maximum of digits after '+' (15). */
export const PHONE_MAX_DIGITS = 15;

/** KZ_MAX_DIGITS is country code 7 plus 10 national digits. */
export const KZ_MAX_DIGITS = 11;

/** PhoneEditResult is the field state after one keystroke or paste. */
export type PhoneEditResult = {
  /** e164 is '+' and digits (or '' / '+' while still empty). */
  e164: string;
  /** display is what the TextInput shows. */
  display: string;
  /** caret is the UTF-16 offset to place the cursor at in display. */
  caret: number;
};

/** digitsOf returns only the digit characters of input. */
export function digitsOf(input: string): string {
  return input.replace(/\D/g, '');
}

/**
 * toE164 turns typed or pasted text into '+' and digits.
 * A leading 8 or 7 without '+' becomes +7 (KZ trunk prefix / country code).
 * Digits are capped at the KZ or E.164 maximum.
 */
export function toE164(input: string): string {
  const trimmed = input.trim();
  if (!trimmed) return '';
  const explicitPlus = trimmed.includes('+');
  let digits = digitsOf(trimmed);
  if (!digits) return explicitPlus ? '+' : '';

  if (!explicitPlus) {
    if (digits.startsWith('8')) {
      digits = '7' + digits.slice(1);
    }
  }

  digits = clampDigits(digits);
  return '+' + digits;
}

/** formatPhone formats an E.164-shaped value for display while typing. */
export function formatPhone(e164: string): string {
  if (!e164) return '';
  if (e164 === '+') return '+';
  const digits = e164.startsWith('+') ? e164.slice(1) : digitsOf(e164);
  if (!digits) return '+';
  if (digits.startsWith('7')) return formatKz(digits);
  return formatGrouped(digits);
}

/**
 * applyPhoneEdit maps one TextInput change onto E.164, display and caret.
 * selectionStart is the caret in previousDisplay before the edit (tracked via
 * onSelectionChange). Deleting a separator deletes the digit before it.
 */
export function applyPhoneEdit(
  previousDisplay: string,
  nextText: string,
  selectionStart: number = previousDisplay.length,
): PhoneEditResult {
  const prevDigits = digitsOf(previousDisplay);
  const nextDigits = digitsOf(nextText);
  const digitsBeforeCaret = digitsOf(previousDisplay.slice(0, selectionStart)).length;

  let digits: string;
  let caretDigits: number;

  const deletedSeparator =
    nextDigits.length === prevDigits.length && nextText.length < previousDisplay.length && prevDigits.length > 0;

  if (deletedSeparator) {
    // Backspace on a space, ')', '-' or similar: drop the digit before the caret.
    const removeIdx = Math.max(0, digitsBeforeCaret - 1);
    digits = prevDigits.slice(0, removeIdx) + prevDigits.slice(removeIdx + 1);
    caretDigits = removeIdx;
  } else {
    digits = nextDigits;
    const digitDelta = nextDigits.length - prevDigits.length;
    if (digitDelta > 0) {
      caretDigits = digitsBeforeCaret + digitDelta;
    } else if (digitDelta < 0) {
      caretDigits = Math.max(0, digitsBeforeCaret + digitDelta);
    } else {
      caretDigits = digitsBeforeCaret;
    }
  }

  // A '+' in nextText (or still carrying digits from a previous international value)
  // means do not rewrite a leading 8 as the KZ trunk prefix.
  const explicitPlus = nextText.includes('+') || (digits.length > 0 && previousDisplay.includes('+'));
  const e164 = digitsToE164(digits, explicitPlus, nextText);
  const display = formatPhone(e164);
  const maxCaret = digitsOf(e164).length;
  const caret = caretAfterDigits(display, Math.min(Math.max(0, caretDigits), maxCaret));
  return { e164, display, caret };
}

function digitsToE164(digits: string, explicitPlus: boolean, nextText: string): string {
  // Cleared field → ''; a lone '+' stub only when the user still typed/kept '+'.
  if (!digits) return nextText.includes('+') ? '+' : '';
  let d = digits;
  if (!explicitPlus && d.startsWith('8')) {
    d = '7' + d.slice(1);
  }
  d = clampDigits(d);
  return '+' + d;
}

function clampDigits(digits: string): string {
  if (digits.startsWith('7')) return digits.slice(0, KZ_MAX_DIGITS);
  return digits.slice(0, PHONE_MAX_DIGITS);
}

/** formatKz: +7 (XXX) XXX-XX-XX as digits arrive. */
function formatKz(digits: string): string {
  const rest = digits.slice(1);
  let out = '+7';
  if (rest.length === 0) return out;
  out += ' (' + rest.slice(0, 3);
  if (rest.length <= 3) return out;
  out += ') ' + rest.slice(3, 6);
  if (rest.length <= 6) return out;
  out += '-' + rest.slice(6, 8);
  if (rest.length <= 8) return out;
  return out + '-' + rest.slice(8, 10);
}

/** formatGrouped: '+' and digits in groups of three. */
function formatGrouped(digits: string): string {
  const parts: string[] = [];
  for (let i = 0; i < digits.length; i += 3) {
    parts.push(digits.slice(i, i + 3));
  }
  return '+' + parts.join(' ');
}

/** caretAfterDigits returns the display offset just after `count` digits. */
function caretAfterDigits(display: string, count: number): number {
  if (count <= 0) return display.startsWith('+') ? 1 : 0;
  let seen = 0;
  for (let i = 0; i < display.length; i++) {
    if (display[i]! >= '0' && display[i]! <= '9') {
      seen++;
      if (seen === count) return i + 1;
    }
  }
  return display.length;
}
