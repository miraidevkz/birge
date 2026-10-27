// Checks the web export (dist/) is an installable shell. Runs after
// `expo export --platform web` in `npm run web-build`.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const dist = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist');
const problems = [];

const indexPath = join(dist, 'index.html');
const html = existsSync(indexPath) ? readFileSync(indexPath, 'utf8') : '';
if (!html) problems.push('dist/index.html is missing');
if (html && !html.includes('rel="manifest" href="/manifest.webmanifest"')) {
  problems.push('dist/index.html does not link /manifest.webmanifest');
}

const manifestPath = join(dist, 'manifest.webmanifest');
if (!existsSync(manifestPath)) {
  problems.push('dist/manifest.webmanifest is missing');
} else {
  const m = JSON.parse(readFileSync(manifestPath, 'utf8'));
  if (!m.name || !m.short_name) problems.push('manifest: name and short_name are required');
  if (m.display !== 'standalone') problems.push('manifest: display must be "standalone"');
  if (!Array.isArray(m.icons) || m.icons.length === 0) problems.push('manifest: icons are required');
  for (const icon of m.icons ?? []) {
    if (!existsSync(join(dist, icon.src))) problems.push(`manifest: icon ${icon.src} is missing from dist/`);
  }
}

const cssDir = join(dist, '_expo', 'static', 'css');
const css = existsSync(cssDir)
  ? readdirSync(cssDir)
      .filter((f) => f.endsWith('.css'))
      .map((f) => readFileSync(join(cssDir, f), 'utf8'))
      .join('\n')
  : '';
for (const needle of ['--color-background', '--color-primary', 'prefers-color-scheme:dark']) {
  if (!css.replace(/\s+/g, '').includes(needle)) problems.push(`CSS does not contain ${needle}`);
}

if (problems.length > 0) {
  for (const p of problems) console.error(`web-build: ${p}`);
  process.exit(1);
}
console.log('web-build: dist/ has index.html, the manifest, its icons and the token CSS variables.');
