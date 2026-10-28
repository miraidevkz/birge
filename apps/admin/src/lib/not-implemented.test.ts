import { ApiError, NetworkError } from '@birge/api';
import { describe, expect, it } from '@jest/globals';

import { isForbidden, isNotImplemented } from './not-implemented';

describe('isNotImplemented', () => {
  it('is true for the 501 answer of a route the server does not serve yet', () => {
    expect(isNotImplemented(new ApiError(501, 'not_implemented', 'Not implemented'))).toBe(true);
    expect(isNotImplemented(new ApiError(501, 'unknown', 'HTTP 501'))).toBe(true);
  });

  it('is true for the 404 of a route that is not mounted (no not_found problem code)', () => {
    expect(isNotImplemented(new ApiError(404, 'unknown', 'HTTP 404'))).toBe(true);
    expect(isNotImplemented(new ApiError(404, 'route_missing', 'not mounted'))).toBe(true);
  });

  it('is false for a real not_found answer and for other errors', () => {
    expect(isNotImplemented(new ApiError(404, 'not_found', 'not found'))).toBe(false);
    expect(isNotImplemented(new ApiError(500, 'internal', 'server error'))).toBe(false);
    expect(isNotImplemented(new ApiError(403, 'forbidden', 'forbidden'))).toBe(false);
    expect(isNotImplemented(new NetworkError())).toBe(false);
    expect(isNotImplemented(new Error('x'))).toBe(false);
    expect(isNotImplemented(null)).toBe(false);
  });
});

describe('isForbidden', () => {
  it('is true for a role denial, false for mfa_required and other errors', () => {
    expect(isForbidden(new ApiError(403, 'forbidden', 'forbidden'))).toBe(true);
    expect(isForbidden(new ApiError(403, 'mfa_required', 'mfa'))).toBe(false);
    expect(isForbidden(new ApiError(404, 'not_found', 'not found'))).toBe(false);
    expect(isForbidden(new Error('x'))).toBe(false);
  });
});
