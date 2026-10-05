import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import * as fs from 'node:fs/promises';
import path from 'node:path';

export const CONTRACT = 'esf/pi/v1';
export const SUMMARY_LIMIT = 128 * 1024;
export const sha256 = value => createHash('sha256').update(value).digest('hex');

export async function readJSON(file, maxBytes = SUMMARY_LIMIT) {
  const handle = await fs.open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const stat = await handle.stat();
    if (!stat.isFile() || stat.size > maxBytes) throw new Error('invalid or oversized Pi state receipt');
    return JSON.parse(await handle.readFile('utf8'));
  } finally { await handle.close(); }
}

export function validateRequest(r) {
  const digest = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
  if (!r || r.contract_version !== CONTRACT || !r.run_id || typeof r.prompt !== 'string' || !r.prompt.trim() ||
      !digest(r.fingerprint) || !digest(r.runner_sha256) || !digest(r.runtime_sha256) ||
      r.request_id !== sha256(`${r.run_id}\0${r.prompt}`) ||
      !path.isAbsolute(r.repository_dir) || path.normalize(r.repository_dir) !== r.repository_dir ||
      r.state_dir !== path.join(path.dirname(r.repository_dir), '.factory', 'pi', sha256(r.run_id)) ||
      !Number.isSafeInteger(r.timeout_ms) || r.timeout_ms <= 0 || r.timeout_ms > 2_147_483_647 ||
      ![0, 1].includes(r.max_process_restarts) ||
      !['openai-completions', 'openai-responses'].includes(r.api)) throw new Error('invalid Pi request contract');
  const endpoint = new URL(r.base_url);
  if (!['http:', 'https:'].includes(endpoint.protocol) || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error('invalid Pi endpoint');
  const m = r.metadata;
  if (!m || m.id !== r.model || m.api !== r.api || !Number.isSafeInteger(m.contextWindow) ||
      !Number.isSafeInteger(m.maxTokens) || m.maxTokens <= 0 || m.contextWindow <= m.maxTokens ||
      m.input?.length !== 1 || m.input[0] !== 'text') throw new Error('invalid pinned model definition');
  if (r.thinking_level !== 'off' && (!m.reasoning || !m.thinkingLevels?.includes(r.thinking_level))) throw new Error('unsupported model reasoning level');
  if (m.pricingKnown && !m.cost) throw new Error('known pricing requires rates');
  if (m.cost && ['input', 'output', 'cacheRead', 'cacheWrite'].some(k => !Number.isFinite(m.cost[k]) || m.cost[k] < 0)) throw new Error('invalid model cost');
  return r;
}

// Sidecars are scratch. Atomic replacement and fsync prevent a process crash
// from presenting a half-written receipt as a completed result.
export async function writeJSON(file, value, maxBytes = SUMMARY_LIMIT) {
  const bytes = JSON.stringify(value) + '\n';
  if (Buffer.byteLength(bytes) > maxBytes) throw new Error('Pi summary exceeds size limit');
  const temp = `${file}.${process.pid}.tmp`;
  const handle = await fs.open(temp, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
  try { await handle.writeFile(bytes); await handle.sync(); } finally { await handle.close(); }
  try { await fs.rename(temp, file); } finally { await fs.rm(temp, { force: true }); }
  const directory = await fs.open(path.dirname(file), constants.O_RDONLY);
  try { await directory.sync(); } finally { await directory.close(); }
}

export async function prepareState(r) {
  await fs.mkdir(r.state_dir, { recursive: true, mode: 0o700 });
  if (!(await fs.lstat(r.state_dir)).isDirectory() || (await fs.lstat(r.state_dir)).isSymbolicLink()) throw new Error('invalid Pi state directory');
  // Never steal a stale lock. Supervisor death is handled by factory cleanup,
  // not by guessing whether a previous process or its tools are still active.
  const lock = await fs.open(path.join(r.state_dir, 'owner.lock'), 'wx', 0o600);
  try {
    await lock.writeFile(String(process.pid)); await lock.sync();
    const identityPath = path.join(r.state_dir, 'identity.json');
    const expected = { ...r };
    let previous;
    try { previous = await readJSON(identityPath, 4 * 1024 * 1024); } catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (previous) {
      if (JSON.stringify(previous) !== JSON.stringify(expected)) throw new Error('Pi session fingerprint or configuration mismatch');
      const stat = await fs.lstat(path.join(r.state_dir, 'journal', 'main.jsonl'));
      if (!stat.isFile() || stat.isSymbolicLink() || stat.size === 0) throw new Error('Pi journal is missing or invalid');
    } else {
      // An orphan journal is not a new session. Never adopt state whose
      // original request/configuration identity has been lost.
      try { await fs.lstat(path.join(r.state_dir, 'journal')); throw new Error('Pi journal has no session identity'); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      await writeJSON(identityPath, expected, 4 * 1024 * 1024);
    }
    await fs.rm(path.join(r.state_dir, 'terminal.json'), { force: true });
    return async () => { await lock.close(); await fs.unlink(path.join(r.state_dir, 'owner.lock')); };
  } catch (error) {
    await lock.close(); await fs.rm(path.join(r.state_dir, 'owner.lock'), { force: true }); throw error;
  }
}

// Read-only, conservative process inspection. PID plus start time protects
// against PID reuse. A disappearing/unreadable process is uncertain, not absent.
export async function processInventory() {
  const found = new Map();
  for (const pid of (await fs.readdir('/proc')).filter(name => /^\d+$/.test(name))) {
    const stat = await fs.readFile(`/proc/${pid}/stat`, 'utf8');
    const fields = stat.slice(stat.lastIndexOf(')') + 2).trim().split(/\s+/);
    if (fields.length < 20) throw new Error('unreadable process inventory');
    found.set(Number(pid), fields[19]); // proc stat field 22, after pid and comm
  }
  return found;
}

export function extraProcesses(baseline, current) {
  return [...current].filter(([pid, start]) => baseline.get(pid) !== start).map(([pid]) => pid);
}

export function summarizeUsage(r, ledger, uncertain) {
  const models = ledger?.models ?? {};
  const tools = ledger?.tools ?? {};
  const values = Object.values(models);
  const valid = values.length > 0 && values.every(u =>
    ['input', 'output', 'cacheRead', 'cacheWrite', 'totalTokens'].every(k => Number.isSafeInteger(u[k]) && u[k] >= 0) && u.totalTokens > 0);
  const tokensIn = valid ? values.reduce((n, u) => n + u.input + u.cacheRead + u.cacheWrite, 0) : null;
  const tokensOut = valid ? values.reduce((n, u) => n + u.output, 0) : null;
  const complete = !uncertain && valid && Number.isSafeInteger(tokensIn) && Number.isSafeInteger(tokensOut);
  const estimate = complete && r.metadata.pricingKnown && values.every(u => Number.isFinite(u.cost?.total) && u.cost.total >= 0)
    ? values.reduce((n, u) => n + u.cost.total, 0) : null;
  const cost = Number.isFinite(estimate) ? estimate : null;
  return { contract_version: CONTRACT, run_id: r.run_id, fingerprint: r.fingerprint, complete,
    tokens_in: complete ? tokensIn : null, tokens_out: complete ? tokensOut : null,
    cost_usd: cost, cost_basis: cost === null ? 'unavailable' : 'estimated',
    recorded_tokens_in: tokensIn, recorded_tokens_out: tokensOut,
    uncertainty: uncertain ? 'interrupted or incomplete provider usage; recorded measurements are a known floor' : valid ? null : 'provider usage unavailable', models, tools };
}
