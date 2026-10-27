import { describe, expect, it } from '@jest/globals';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';

// The same check as `npm run api-check`, inside the test suite so a stale
// client fails `npm test` too.
describe('schema.gen.ts', () => {
  it('matches what openapi-typescript generates from contracts/openapi/birge.yaml', () => {
    const script = join(__dirname, '..', 'scripts', 'check-generated.mjs');
    const run = spawnSync(process.execPath, [script], { encoding: 'utf8' });
    expect({ status: run.status, stderr: run.stderr }).toEqual({ status: 0, stderr: '' });
  }, 60_000);
});
