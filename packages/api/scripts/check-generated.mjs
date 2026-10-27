// Fails when src/schema.gen.ts is not what openapi-typescript generates from
// the current contract, so a contract change cannot land without regenerating
// the client types (ADR-0005: generated code is regenerated, never edited).
// Used by `npm run api-check` and by src/schema.gen.test.ts.
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const pkg = join(here, '..');
const spec = join(pkg, '..', '..', 'contracts', 'openapi', 'birge.yaml');
const committed = join(pkg, 'src', 'schema.gen.ts');

// Resolve the CLI from this package so the pinned version is the one used.
const require = createRequire(join(pkg, 'package.json'));
const cli = join(dirname(require.resolve('openapi-typescript/package.json')), 'bin', 'cli.js');

const dir = mkdtempSync(join(tmpdir(), 'birge-api-check-'));
try {
  const fresh = join(dir, 'schema.gen.ts');
  execFileSync(process.execPath, [cli, spec, '--output', fresh], { stdio: ['ignore', 'ignore', 'inherit'] });
  if (readFileSync(fresh, 'utf8') !== readFileSync(committed, 'utf8')) {
    console.error('packages/api/src/schema.gen.ts is stale: run `npm run api-gen` and commit the result.');
    process.exit(1);
  }
  console.log('packages/api/src/schema.gen.ts is up to date.');
} finally {
  rmSync(dir, { recursive: true, force: true });
}
