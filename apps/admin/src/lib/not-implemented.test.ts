import { ApiError, NetworkError } from '@birge/api';
import { describe, expect, it } from '@jest/globals';

import { isNotImplemented } from './not-implemented';

describe('isNotImplemented', () => {
  it('is true for the 501 answer of a route the server does not serve yet', () => {
    expect(isNotImplemented(new ApiError(501, 'not_implemented', 'Not implemented'))).toBe(true);
    expect(isNotImplemented(new ApiError(501, 'unknown', 'HTTP 501'))).toBe(true);
  });

  it('is false for other errors', () => {
    expect(isNotImplemented(new ApiError(500, 'internal', 'server error'))).toBe(false);
    expect(isNotImplemented(new ApiError(404, 'not_found', 'not found'))).toBe(false);
    expect(isNotImplemented(new NetworkError())).toBe(false);
    expect(isNotImplemented(new Error('x'))).toBe(false);
    expect(isNotImplemented(null)).toBe(false);
  });
});
