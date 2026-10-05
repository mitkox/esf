import { spawn } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { CONTRACT, extraProcesses, prepareState, processInventory, readJSON, summarizeUsage, validateRequest, writeJSON } from './protocol.mjs';

export async function supervise(request, executable, options = {}) {
  const r = validateRequest(request);
  const release = await prepareState(r);
  const started = performance.now();
  const inspect = options.inventory ?? processInventory;
  let ledger = { models: {}, tools: {} }, uncertain = false, restarts = 0, stopped;
  const attempts = [];
  let activeChild;
  const stop = reason => {
    stopped ??= reason;
    activeChild?.send({ type: 'stop' }, () => {});
    const child = activeChild;
    const kill = setTimeout(() => { if (child?.exitCode === null && child?.signalCode === null) child.kill('SIGKILL'); }, 1_000);
    kill.unref();
  };
  const cancel = () => stop('cancelled');
  process.on('SIGTERM', cancel); process.on('SIGINT', cancel);
  const timer = setTimeout(() => stop('timeout'), r.timeout_ms);
  const log = options.log ?? (event => process.stdout.write(JSON.stringify(event) + '\n'));
  const base = { contract_version: CONTRACT, run_id: r.run_id, request_id: r.request_id, fingerprint: r.fingerprint, model: r.model };
  let result;
  try {
    try {
      const previous = await readJSON(path.join(r.state_dir, 'usage.json'));
      if (previous.contract_version !== CONTRACT || previous.run_id !== r.run_id || previous.fingerprint !== r.fingerprint) throw new Error('Pi usage identity mismatch');
      uncertain = previous.complete !== true;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      // A reused journal without its completeness receipt cannot establish
      // whether earlier provider attempts had missing metering.
      try { await fs.lstat(path.join(r.state_dir, 'journal')); uncertain = true; }
      catch (missing) { if (missing.code !== 'ENOENT') throw missing; }
    }
    const baseline = await inspect();
    for (;;) {
      const remaining = r.timeout_ms - (performance.now() - started);
      if (stopped || remaining <= 0) { stopped ??= 'timeout'; result = { status: stopped, error: stopped }; break; }
      if (restarts > 0) {
        const stat = await fs.lstat(path.join(r.state_dir, 'journal', 'main.jsonl'));
        if (!stat.isFile() || stat.isSymbolicLink() || stat.size === 0) throw new Error('cannot recover missing Pi journal');
      }
      for (const [file, expected] of [[process.execPath, r.runtime_sha256], [executable, r.runner_sha256]]) {
        const hash = createHash('sha256');
        for await (const bytes of createReadStream(file)) hash.update(bytes);
        if (hash.digest('hex') !== expected) throw new Error('Pi execution-chain digest mismatch');
      }
      // The deadline can expire during async file hashing, before a child
      // exists to receive stop(). Never launch work after that notification.
      if (stopped || performance.now() - started >= r.timeout_ms) {
        stopped ??= 'timeout'; result = { status: stopped, error: stopped }; break;
      }
      let terminal, fatal, protocolError;
      const activeShell = new Set();
      const child = spawn(process.execPath, [...process.execArgv, executable, '--engine'], {
        cwd: r.repository_dir, stdio: ['pipe', 'pipe', 'pipe', 'ipc'],
        env: { ...process.env },
      });
      activeChild = child;
      log({ ...base, type: 'engine_started', pid: child.pid, attempt: restarts + 1 });
      child.stdout.pipe(process.stdout, { end: false });
      child.stderr.pipe(process.stderr, { end: false });
      child.on('message', message => {
        try {
          if (!message || typeof message !== 'object') throw new Error('invalid engine message');
          if (message.type === 'shell-start') { if (!message.call_id || activeShell.has(message.call_id)) throw new Error('invalid shell ownership'); activeShell.add(message.call_id); log({ ...base, type: 'shell_guard', phase: 'active', call_id: message.call_id }); }
          else if (message.type === 'shell-end') { if (!activeShell.delete(message.call_id)) throw new Error('unknown shell ownership'); log({ ...base, type: 'shell_guard', phase: 'released', call_id: message.call_id }); }
          else if (message.type === 'usage-incomplete') uncertain = true;
          else if (message.type === 'usage') ledger = message.ledger;
          else if (message.type === 'audit') log({ ...base, type: 'agent_event', attempt: restarts + 1, event: message.event });
          else if (message.type === 'terminal') { if (terminal || !['completed', 'failed'].includes(message.result?.status)) throw new Error('invalid terminal result'); terminal = message.result; ledger = terminal.ledger; }
          else if (message.type === 'fatal') fatal = String(message.error ?? 'Pi engine failed');
          else throw new Error('unknown engine message');
          if (message.id !== undefined) child.send({ type: 'ack', id: message.id }, () => {});
        } catch (error) { protocolError = error.message; child.kill('SIGKILL'); }
      });
      const exited = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal })); });
      child.stdin.end(JSON.stringify(r));
      const exit = await exited;
      activeChild = undefined;
      attempts.push({ attempt: restarts + 1, ...exit, active_shell: activeShell.size > 0 });
      if (stopped) { uncertain = true; result = { status: stopped, error: stopped }; break; }
      if (protocolError || fatal) { uncertain = true; result = { status: 'failed', error: protocolError ?? fatal }; break; }
      if (terminal) { result = terminal; uncertain ||= terminal.status !== 'completed'; break; }
      uncertain = true; // a provider may have billed a lost response
      if (activeShell.size) { result = { status: 'failed', error: 'engine crashed during shell execution; recovery refused' }; break; }
      const additional = extraProcesses(baseline, await inspect());
      if (additional.length) { result = { status: 'failed', error: 'live additional processes remain; recovery refused' }; break; }
      if (restarts >= r.max_process_restarts) { result = { status: 'failed', error: 'Pi process restart allowance exhausted' }; break; }
      restarts++;
      log({ ...base, type: 'process_recovery', restarts });
    }
  } catch (error) {
    uncertain = true; result = { status: stopped ?? 'failed', error: error.message };
  } finally {
    clearTimeout(timer); process.off('SIGTERM', cancel); process.off('SIGINT', cancel);
  }
  try {
    await writeJSON(path.join(r.state_dir, 'usage.json'), summarizeUsage(r, ledger, uncertain));
    await writeJSON(path.join(r.state_dir, 'recovery.json'), { ...base, restarts, attempts, error: result.error });
    await writeJSON(path.join(r.state_dir, 'terminal.json'), { ...base, status: result.status, error: result.error, restarts });
    log({ ...base, type: 'terminal', status: result.status, restarts });
    return result.status === 'completed' ? 0 : 1;
  } finally { await release(); }
}
