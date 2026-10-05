import * as fs from 'node:fs/promises';
import path from 'node:path';

// Include notices for the modules actually bundled, plus the pinned runtime.
// Read only installed packages from the frozen dependency graph.
export async function writeNotices(inputs) {
  const packages = new Map();
  for (const input of inputs.filter(file => file.includes('node_modules/'))) {
    let directory = path.dirname(path.resolve(input));
    for (;;) {
      try {
        const pkg = JSON.parse(await fs.readFile(path.join(directory, 'package.json'), 'utf8'));
        if (pkg.name && pkg.version) { packages.set(pkg.name, { directory, pkg }); break; }
      } catch (error) { if (error.code !== 'ENOENT') throw error; }
      const parent = path.dirname(directory);
      if (parent === directory) throw new Error(`missing package identity for ${input}`);
      directory = parent;
    }
  }
  const sections = ['ESF Pi runner: bundled dependency and runtime notices\n'];
  for (const [name, { directory, pkg }] of [...packages].sort(([a], [b]) => a.localeCompare(b, 'en'))) {
    const notices = [];
    if (name.startsWith('@earendil-works/')) notices.push(await fs.readFile('LICENSE.pi', 'utf8'));
    else {
      const files = (await fs.readdir(directory)).filter(file => /^(license|licence|copying|notice)([._-].*)?$/i.test(file)).sort();
      for (const file of files) {
        if ((await fs.stat(path.join(directory, file))).isFile()) notices.push(await fs.readFile(path.join(directory, file), 'utf8'));
      }
    }
    if (!notices.length) throw new Error(`missing bundled license notice for ${name}@${pkg.version}`);
    sections.push(`===== ${name}@${pkg.version} (${pkg.license ?? 'see notice'}) =====\n${notices.join('\n')}`);
  }
  sections.push(`===== Node.js 24.21.0 =====\n${await fs.readFile('LICENSE.node', 'utf8')}`);
  await fs.writeFile('dist/THIRD_PARTY_NOTICES.txt', sections.join('\n\n') + '\n');
}
