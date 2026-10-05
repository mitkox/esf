import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import * as fs from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import os from 'node:os';
import { once } from 'node:events';
import { test } from 'node:test';
import { CONTRACT, sha256 } from '../src/protocol.mjs';
import { supervise } from '../src/supervisor.mjs';

const runner = path.resolve('dist/pi-runner.mjs');
const runnerDigest = sha256(await fs.readFile(runner));
const runtimeDigest = sha256(await fs.readFile(process.execPath));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(predicate, timeout = 10_000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (predicate()) return; await delay(10); }
  throw new Error('test condition timed out');
}
async function fixture(handler) {
  const base = await fs.mkdtemp(path.join(os.tmpdir(), 'esf-pi-test-'));
  const repo = path.join(base, 'repo'); await fs.mkdir(repo);
  let calls = 0;
  const server = createServer(async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    const body = JSON.parse(Buffer.concat(chunks));
    calls++;
    try { await handler({ req, res, body, calls, repo }); } catch (error) { res.destroy(error); }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const runID = path.basename(base);
  const request = { contract_version: CONTRACT, run_id: runID, request_id: sha256(`${runID}\0Fix the code`),
    prompt: 'Fix the code', repository_dir: repo, state_dir: path.join(base, '.factory', 'pi', sha256(runID)),
    provider: 'local', base_url: `http://127.0.0.1:${server.address().port}/v1`, model: 'test-model',
    api: 'openai-completions', thinking_level: 'off', timeout_ms: 10_000, max_process_restarts: 1,
    runner_sha256: runnerDigest, runtime_sha256: runtimeDigest,
    metadata: { id: 'test-model', name: 'Test model', api: 'openai-completions', reasoning: false,
      input: ['text'], contextWindow: 32_768, maxTokens: 1_024, pricingKnown: true,
      cost: { input: 1, output: 2, cacheRead: 0.1, cacheWrite: 1 }, thinkingLevels: ['off'] },
    fingerprint: 'c'.repeat(64) };
  return { request, repo, calls: () => calls, async close() { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); await fs.rm(base, { recursive: true, force: true }); } };
}
function answer(res, content = 'Done', options = {}) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const chunk = (delta, finish_reason = null) => ({ id: 'chat-1', object: 'chat.completion.chunk', created: 1, model: 'test-model', choices: [{ index: 0, delta, finish_reason }] });
  res.write(`data: ${JSON.stringify(chunk({ role: 'assistant', ...(options.tool ? { tool_calls: [{ index: 0, id: 'tool-1', type: 'function', function: { name: options.tool.name, arguments: JSON.stringify(options.tool.args) } }] } : { content }) }))}\n\n`);
  res.write(`data: ${JSON.stringify(chunk({}, options.tool ? 'tool_calls' : 'stop'))}\n\n`);
  if (!options.missingUsage) res.write(`data: ${JSON.stringify({ ...chunk({}), choices: [], usage: { prompt_tokens: 12, completion_tokens: 5, prompt_tokens_details: { cached_tokens: 2 } } })}\n\n`);
  res.end('data: [DONE]\n\n');
}
function launch(request) {
  const events = []; let stderr = '', pending = '';
  const child = spawn(process.execPath, [runner], { stdio: ['pipe', 'pipe', 'pipe'], env: { ...process.env, ESF_PI_API_KEY: 'test-placeholder' } });
  child.stdout.on('data', chunk => { pending += chunk; let newline; while ((newline = pending.indexOf('\n')) >= 0) { const line = pending.slice(0, newline); pending = pending.slice(newline + 1); try { events.push(JSON.parse(line)); } catch {} } });
  child.stderr.on('data', chunk => { stderr += chunk; });
  child.stdin.end(JSON.stringify(request));
  const finished = once(child, 'exit').then(([code, signal]) => ({ code, signal, events, stderr }));
  return { child, events, finished };
}
async function summary(f, name) { return JSON.parse(await fs.readFile(path.join(f.request.state_dir, name), 'utf8')); }

test('explicit Responses protocol preserves committed usage', async () => {
  const f = await fixture(({ req, res }) => {
    assert.equal(req.url, '/v1/responses');
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const emit = event => res.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
    const part = { type: 'output_text', text: 'Done', annotations: [] };
    const item = { id: 'msg_1', type: 'message', role: 'assistant', status: 'completed', content: [part] };
    const response = { id: 'resp_1', object: 'response', created_at: 1, model: 'test-model', status: 'completed', output: [item], usage: { input_tokens: 12, output_tokens: 5, total_tokens: 17, input_tokens_details: { cached_tokens: 2 }, output_tokens_details: { reasoning_tokens: 1 } } };
    emit({ type: 'response.created', response: { ...response, output: [], status: 'in_progress' } });
    emit({ type: 'response.output_item.added', output_index: 0, item: { ...item, content: [], status: 'in_progress' } });
    emit({ type: 'response.content_part.added', output_index: 0, content_index: 0, item_id: item.id, part: { ...part, text: '' } });
    emit({ type: 'response.output_text.delta', output_index: 0, content_index: 0, item_id: item.id, delta: 'Done' });
    emit({ type: 'response.output_text.done', output_index: 0, content_index: 0, item_id: item.id, text: 'Done' });
    emit({ type: 'response.output_item.done', output_index: 0, item });
    emit({ type: 'response.completed', response }); res.end();
  });
  f.request.api = f.request.metadata.api = 'openai-responses';
  try {
    const result = await launch(f.request).finished; assert.equal(result.code, 0, result.stderr);
    const usage = await summary(f, 'usage.json'); assert.equal(usage.tokens_in, 12); assert.equal(usage.tokens_out, 5);
  } finally { await f.close(); }
});

test('completion, commit audit, cached tokens, estimated cost, and submission deduplication', async () => {
  const f = await fixture(({ res }) => answer(res));
  try {
    const first = await launch(f.request).finished;
    assert.equal(first.code, 0, first.stderr);
    assert.ok(first.events.some(e => e.type === 'agent_event' && e.event.type === 'message_end'));
    const usage = await summary(f, 'usage.json');
    assert.equal(usage.complete, true); assert.equal(usage.tokens_in, 12); assert.equal(usage.tokens_out, 5);
    assert.equal(usage.cost_basis, 'estimated'); assert.ok(usage.cost_usd > 0);
    const again = await launch(f.request).finished;
    assert.equal(again.code, 0, again.stderr); assert.equal(f.calls(), 1);
  } finally { await f.close(); }
});

test('confirmed engine death during a model request resumes the same submission with partial usage', async () => {
  const f = await fixture(({ res, calls }) => { if (calls > 1) answer(res); });
  try {
    const run = launch(f.request);
    await until(() => f.calls() === 1 && run.events.some(e => e.type === 'engine_started'));
    process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    const result = await run.finished;
    assert.equal(result.code, 0, result.stderr); assert.equal(f.calls(), 2);
    assert.equal((await summary(f, 'terminal.json')).restarts, 1);
    const usage = await summary(f, 'usage.json'); assert.equal(usage.complete, false); assert.equal(usage.cost_usd, null);
    assert.equal(usage.tokens_in, null); assert.ok(usage.recorded_tokens_in > 0);
  } finally { await f.close(); }
});

test('engine death during shell execution fails without replaying the command', async () => {
  const f = await fixture(({ res }) => answer(res, '', { tool: { name: 'bash', args: { command: 'sleep 20' } } }));
  try {
    const run = launch(f.request);
    await until(() => run.events.some(e => e.type === 'shell_guard' && e.phase === 'active'));
    process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    const result = await run.finished;
    assert.equal(result.code, 1); assert.equal(f.calls(), 1);
    assert.match((await summary(f, 'terminal.json')).error, /shell execution/);
    assert.equal((await summary(f, 'terminal.json')).restarts, 0);
  } finally { await f.close(); }
});

test('normal provider failure is terminal and does not restart the engine', async () => {
  const f = await fixture(({ res }) => { res.writeHead(400, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error: { message: 'test provider rejected request' } })); });
  try {
    const result = await launch(f.request).finished;
    assert.equal(result.code, 1); assert.equal(f.calls(), 1); assert.equal((await summary(f, 'terminal.json')).restarts, 0);
  } finally { await f.close(); }
});

test('missing provider usage stays unavailable rather than zero', async () => {
  const f = await fixture(({ res }) => answer(res, 'Done', { missingUsage: true }));
  try { const result = await launch(f.request).finished; assert.equal(result.code, 0, result.stderr); const usage = await summary(f, 'usage.json'); assert.equal(usage.complete, false); assert.equal(usage.cost_usd, null); assert.equal(usage.tokens_in, null); }
  finally { await f.close(); }
});

test('missing usage in an earlier turn remains unavailable on a duplicate submission', async () => {
  const f = await fixture(({ res, calls }) => calls === 1
    ? answer(res, '', { missingUsage: true, tool: { name: 'write', args: { path: 'hello.txt', content: 'hello' } } })
    : answer(res));
  try {
    assert.equal((await launch(f.request).finished).code, 0);
    assert.equal((await summary(f, 'usage.json')).complete, false);
    assert.equal((await launch(f.request).finished).code, 0); assert.equal(f.calls(), 2);
    assert.equal((await summary(f, 'usage.json')).complete, false);
  } finally { await f.close(); }
});

test('owner lock rejects concurrent invocations without stealing a live session', async () => {
  const f = await fixture(() => {});
  try {
    const first = launch(f.request); await until(() => f.calls() === 1);
    const second = await launch(f.request).finished; assert.equal(second.code, 1); assert.match(second.stderr, /EEXIST/); assert.equal(f.calls(), 1);
    first.child.kill('SIGTERM'); const result = await first.finished; assert.equal(result.code, 1); assert.equal((await summary(f, 'terminal.json')).status, 'cancelled');
  } finally { await f.close(); }
});

test('deadline remains bounded and never restarts on timeout', async () => {
  const f = await fixture(() => {}); f.request.timeout_ms = 500;
  try {
    const start = Date.now(); const result = await launch(f.request).finished;
    assert.equal(result.code, 1); assert.ok(Date.now() - start < 3000);
    const terminal = await summary(f, 'terminal.json'); assert.equal(terminal.status, 'timeout'); assert.equal(terminal.restarts, 0);
  } finally { await f.close(); }
});

test('a deadline expiring during digest verification never launches an engine', async () => {
  const f = await fixture(() => {}); f.request.timeout_ms = 1;
  try {
    const result = await launch(f.request).finished; assert.equal(result.code, 1);
    assert.equal(f.calls(), 0); assert.equal(result.events.filter(e => e.type === 'engine_started').length, 0);
    assert.equal((await summary(f, 'terminal.json')).status, 'timeout');
  } finally { await f.close(); }
});

test('session mismatch and missing journal fail without a fresh conversation', async () => {
  const f = await fixture(({ res }) => answer(res));
  try {
    assert.equal((await launch(f.request).finished).code, 0);
    const changed = { ...f.request, prompt: 'Different task' }; changed.request_id = sha256(`${changed.run_id}\0${changed.prompt}`);
    const mismatch = await launch(changed).finished; assert.equal(mismatch.code, 1); assert.match(mismatch.stderr, /fingerprint/);
    await fs.rm(path.join(f.request.state_dir, 'journal'), { recursive: true });
    assert.equal((await launch(f.request).finished).code, 1); assert.equal(f.calls(), 1);
  } finally { await f.close(); }
});

test('committed completion survives death before the supervisor terminal notification', async () => {
  const f = await fixture(({ res }) => answer(res));
  try {
    let pid, killed = false;
    const result = await supervise(f.request, runner, { log: event => {
      if (event.type === 'engine_started') pid = event.pid;
      if (!killed && event.type === 'agent_event' && event.event.type === 'submission' && event.event.record.status === 'done') {
        killed = true; process.kill(pid, 'SIGKILL');
      }
    } });
    assert.equal(result, 0); assert.equal(killed, true); assert.equal(f.calls(), 1);
    assert.equal((await summary(f, 'terminal.json')).restarts, 1);
  } finally { await f.close(); }
});

test('unsafe interrupted file tool is recorded instead of replayed', async () => {
  const f = await fixture(({ res, calls, body }) => {
    if (calls === 1) answer(res, '', { tool: { name: 'read', args: { path: 'waiting' } } });
    else { assert.match(JSON.stringify(body.messages), /interrupted/i); answer(res); }
  });
  try {
    const fifo = spawn('mkfifo', [path.join(f.repo, 'waiting')]); await once(fifo, 'exit');
    const run = launch(f.request);
    await until(() => run.events.some(e => e.type === 'agent_event' && e.event.type === 'tool_execution_start'));
    process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    const result = await run.finished;
    assert.equal(result.code, 0, result.stderr); assert.equal(f.calls(), 2);
    assert.equal((await summary(f, 'terminal.json')).restarts, 1);
  } finally { await f.close(); }
});

test('surviving processes and uncertain process inspection prevent recovery', async () => {
  const f = await fixture(() => {}); let leftover;
  try {
    const run = launch(f.request); await until(() => f.calls() === 1);
    leftover = spawn('sleep', ['20']); await once(leftover, 'spawn');
    process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    assert.equal((await run.finished).code, 1);
    assert.match((await summary(f, 'terminal.json')).error, /additional processes/);
    assert.equal(f.calls(), 1);
  } finally { leftover?.kill('SIGKILL'); if (leftover) await once(leftover, 'exit'); await f.close(); }
  const uncertain = await fixture(() => {}); let inspected = 0;
  try {
    const result = supervise(uncertain.request, runner, { inventory: async () => {
      if (++inspected > 1) throw new Error('process inspection uncertain'); return new Map();
    }, log: event => { if (event.type === 'engine_started') until(() => uncertain.calls() === 1).then(() => process.kill(event.pid, 'SIGKILL')); } });
    assert.equal(await result, 1);
    assert.match((await summary(uncertain, 'terminal.json')).error, /inspection uncertain/);
    assert.equal(uncertain.calls(), 1);
  } finally { await uncertain.close(); }
});

test('corrupt journal and exhausted allowance never start a third engine', async () => {
  const corrupt = await fixture(() => {});
  try {
    const run = launch(corrupt.request); await until(() => corrupt.calls() === 1);
    await fs.writeFile(path.join(corrupt.request.state_dir, 'journal', 'main.jsonl'), 'not-json\nnot-json\n');
    process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    assert.equal((await run.finished).code, 1); assert.equal(corrupt.calls(), 1);
  } finally { await corrupt.close(); }
  const f = await fixture(() => {});
  try {
    const run = launch(f.request); await until(() => f.calls() === 1);
    process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    await until(() => f.calls() === 2 && run.events.filter(e => e.type === 'engine_started').length === 2);
    process.kill(run.events.filter(e => e.type === 'engine_started')[1].pid, 'SIGKILL');
    assert.equal((await run.finished).code, 1); assert.equal(f.calls(), 2);
    assert.match((await summary(f, 'terminal.json')).error, /allowance exhausted/);
  } finally { await f.close(); }
});

test('a restart consumes the original deadline', async () => {
  const f = await fixture(() => {}); f.request.timeout_ms = 900;
  try {
    const start = Date.now(); const run = launch(f.request); await until(() => f.calls() === 1);
    await delay(300); process.kill(run.events.find(e => e.type === 'engine_started').pid, 'SIGKILL');
    assert.equal((await run.finished).code, 1); assert.ok(Date.now() - start < 1600);
    const terminal = await summary(f, 'terminal.json'); assert.equal(terminal.status, 'timeout'); assert.equal(terminal.restarts, 1);
  } finally { await f.close(); }
});
