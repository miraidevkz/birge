import { describe, expect, it } from '@jest/globals';

import { wellbeingKey } from './wellbeing-text';

describe('wellbeingKey', () => {
  it('strips the wellbeing namespace prefix when present', () => {
    expect(wellbeingKey('wellbeing.phq4.q1')).toBe('phq4.q1');
    expect(wellbeingKey('wellbeing.feedback.none')).toBe('feedback.none');
  });

  it('leaves short keys unchanged', () => {
    expect(wellbeingKey('phq.1')).toBe('phq.1');
  });
});
