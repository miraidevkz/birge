import { describe, expect, it } from '@jest/globals';

import { roomTitle } from './room-title';
import { formatDate, formatDateTime } from './time';

describe('time', () => {
  it('formats RFC 3339 times in UTC and passes through what does not parse', () => {
    expect(formatDate('2026-10-28T23:30:00+05:00')).toBe('2026-10-28');
    expect(formatDateTime('2026-10-28T08:05:00Z')).toBe('2026-10-28 08:05 UTC');
    expect(formatDate('soon')).toBe('soon');
  });
});

describe('roomTitle', () => {
  it('names district and topic rooms from the shared locale keys', () => {
    expect(roomTitle('chat.room.district.demo_district_a')).toBe('Mothers of Demo district A');
    expect(roomTitle('chat.room.topic.autism')).toBe('Topic: Autism');
  });

  it('falls back to the code for an unknown reference and to the key for an unknown shape', () => {
    expect(roomTitle('chat.room.district.new_district')).toBe('Mothers of new_district');
    expect(roomTitle('chat.room.other')).toBe('chat.room.other');
  });
});
