import { describe, expect, it } from '@jest/globals';

import { applyPhoneEdit, formatPhone, toE164 } from './phone';

describe('toE164', () => {
  it('returns empty for empty input', () => {
    expect(toE164('')).toBe('');
    expect(toE164('   ')).toBe('');
    expect(toE164('()-')).toBe('');
  });

  it('keeps a lone plus while the user starts an international number', () => {
    expect(toE164('+')).toBe('+');
  });

  it('turns a leading 8 into +7 (KZ trunk)', () => {
    expect(toE164('8')).toBe('+7');
    expect(toE164('87000000000')).toBe('+77000000000');
    expect(toE164('8 700 000 00 00')).toBe('+77000000000');
  });

  it('turns a leading 7 into +7', () => {
    expect(toE164('7')).toBe('+7');
    expect(toE164('77000000000')).toBe('+77000000000');
  });

  it('accepts already-international +7 with separators', () => {
    expect(toE164('+7 (700) 000-00-00')).toBe('+77000000000');
    expect(toE164('+77000000000')).toBe('+77000000000');
  });

  it('keeps a foreign country code and does not rewrite a leading 8 after +', () => {
    expect(toE164('+4915123456789')).toBe('+4915123456789');
    expect(toE164('+49 151 234 567 89')).toBe('+4915123456789');
    expect(toE164('+81 90 1234 5678')).toBe('+819012345678');
  });

  it('prefixes + for digits that are not a local 8/7 start', () => {
    expect(toE164('4915123456789')).toBe('+4915123456789');
  });

  it('caps Kazakhstan at 11 digits and others at 15', () => {
    expect(toE164('+77000000000999')).toBe('+77000000000');
    expect(toE164('+491512345678901234')).toBe('+491512345678901');
  });
});

describe('formatPhone', () => {
  it('formats empty and lone plus', () => {
    expect(formatPhone('')).toBe('');
    expect(formatPhone('+')).toBe('+');
  });

  it('formats Kazakhstan as +7 (XXX) XXX-XX-XX while typing', () => {
    expect(formatPhone('+7')).toBe('+7');
    expect(formatPhone('+77')).toBe('+7 (7');
    expect(formatPhone('+770')).toBe('+7 (70');
    expect(formatPhone('+7700')).toBe('+7 (700');
    expect(formatPhone('+77000')).toBe('+7 (700) 0');
    expect(formatPhone('+770000')).toBe('+7 (700) 00');
    expect(formatPhone('+7700000')).toBe('+7 (700) 000');
    expect(formatPhone('+77000000')).toBe('+7 (700) 000-0');
    expect(formatPhone('+770000000')).toBe('+7 (700) 000-00');
    expect(formatPhone('+7700000000')).toBe('+7 (700) 000-00-0');
    expect(formatPhone('+77000000000')).toBe('+7 (700) 000-00-00');
  });

  it('groups foreign numbers by threes after +', () => {
    expect(formatPhone('+49')).toBe('+49');
    expect(formatPhone('+491')).toBe('+491');
    expect(formatPhone('+4915')).toBe('+491 5');
    expect(formatPhone('+4915123456789')).toBe('+491 512 345 678 9');
  });
});

describe('applyPhoneEdit', () => {
  it('formats digits as the user types at the end', () => {
    let display = '';
    let e164 = '';
    for (const ch of '87000000000') {
      const next = display + ch;
      const result = applyPhoneEdit(display, next, display.length);
      display = result.display;
      e164 = result.e164;
    }
    expect(e164).toBe('+77000000000');
    expect(display).toBe('+7 (700) 000-00-00');
  });

  it('accepts a paste of a formatted KZ number', () => {
    const result = applyPhoneEdit('', '+7 (700) 000-00-00', 0);
    expect(result.e164).toBe('+77000000000');
    expect(result.display).toBe('+7 (700) 000-00-00');
  });

  it('accepts a paste of 8 7xx…', () => {
    const result = applyPhoneEdit('', '8 700 000 00 00', 0);
    expect(result.e164).toBe('+77000000000');
    expect(result.display).toBe('+7 (700) 000-00-00');
  });

  it('accepts a paste of a foreign +49… number', () => {
    const result = applyPhoneEdit('', '+49 151 234 567 89', 0);
    expect(result.e164).toBe('+4915123456789');
    expect(result.display).toBe('+491 512 345 678 9');
  });

  it('deleting a separator deletes the digit before it', () => {
    // "+7 (700) 000-00-00" — caret after ')' (index of ')' is 8, caret 9); backspace removes ')'
    const previous = '+7 (700) 000-00-00';
    const caretAfterParen = previous.indexOf(')') + 1;
    const withoutParen = previous.slice(0, caretAfterParen - 1) + previous.slice(caretAfterParen);
    const result = applyPhoneEdit(previous, withoutParen, caretAfterParen);
    expect(result.e164).toBe('+7700000000');
    expect(result.display).toBe('+7 (700) 000-00-0');
    // caret stays at the digit index that was removed (remaining digits shift left)
    expect(result.display.slice(0, result.caret)).toBe('+7 (70');
  });

  it('deleting a dash deletes the digit before it', () => {
    const previous = '+7 (700) 000-00-00';
    const dashIdx = previous.lastIndexOf('-');
    const caret = dashIdx + 1;
    const withoutDash = previous.slice(0, dashIdx) + previous.slice(dashIdx + 1);
    const result = applyPhoneEdit(previous, withoutDash, caret);
    expect(result.e164).toBe('+7700000000');
    expect(result.display).toBe('+7 (700) 000-00-0');
  });

  it('keeps the caret usable when inserting a digit in the middle', () => {
    // Start from +7 (700) 00-00-00 (missing one digit in the middle block) … use +7700000000
    // Display: +7 (700) 000-00-0 — insert '0' before the last digit to complete
    const previous = '+7 (700) 000-00-0';
    const e164Prev = '+7700000000';
    expect(formatPhone(e164Prev)).toBe(previous);
    // Caret after "000-" (after 7 digits past country? national: 7000000 → 7 digits, caret after third of XXX-XX)
    // Digits: 7,7,0,0,0,0,0,0,0,0 (10 digits) — insert before last
    const caret = previous.length - 1; // before final '0'
    const next = previous.slice(0, caret) + '0' + previous.slice(caret);
    const result = applyPhoneEdit(previous, next, caret);
    expect(result.e164).toBe('+77000000000');
    expect(result.display).toBe('+7 (700) 000-00-00');
    expect(result.caret).toBe(result.display.length - 1);
  });

  it('allows clearing the field', () => {
    const result = applyPhoneEdit('+7 (700) 000-00-00', '', 0);
    expect(result.e164).toBe('');
    expect(result.display).toBe('');
    expect(result.caret).toBe(0);
  });

  it('ignores digits beyond the max length', () => {
    const full = '+7 (700) 000-00-00';
    const result = applyPhoneEdit(full, full + '9', full.length);
    expect(result.e164).toBe('+77000000000');
    expect(result.display).toBe(full);
  });
});
