import { build } from 'esbuild';
import * as fs from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { writeNotices } from './notices.mjs';

const result = await build({ entryPoints: ['src/runner.mjs'], outfile: 'dist/pi-runner.mjs', bundle: true, platform: 'node', metafile: true,
  format: 'esm', target: 'node22.19', sourcemap: false, minify: false, legalComments: 'eof',
  banner: { js: "import { createRequire as esfCreateRequire } from 'node:module'; const require = esfCreateRequire(import.meta.url);" } });
const bytes = await fs.readFile('dist/pi-runner.mjs');
const digest = createHash('sha256').update(bytes).digest('hex');
await fs.writeFile('dist/SHA256SUMS', `${digest}  pi-runner.mjs\n`);
await writeNotices(Object.keys(result.metafile.inputs));
console.log(`Pi runner sha256 ${digest}`);
