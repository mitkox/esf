import { fileURLToPath } from 'node:url';
import { engine } from './engine.mjs';
import { supervise } from './supervisor.mjs';
import { SUMMARY_LIMIT } from './protocol.mjs';

export async function readRequest() {
  const chunks = []; let bytes = 0;
  for await (const chunk of process.stdin) { bytes += chunk.length; if (bytes > 4 * 1024 * 1024) throw new Error('Pi request exceeds size limit'); chunks.push(chunk); }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function engineChannel() {
  let sequence = 0, stopping;
  const pending = new Map();
  process.on('message', message => {
    if (message.type === 'stop') { void stopping?.(); }
    if (message.type === 'ack') { pending.get(message.id)?.(); pending.delete(message.id); }
  });
  process.on('disconnect', () => { void stopping?.(); });
  const notify = (type, payload = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    pending.set(id, resolve);
    process.send({ type, id, ...payload }, error => { if (error) { pending.delete(id); reject(error); } });
  });
  return { notify, onStop: callback => { stopping = callback; },
    audit: event => notify('audit', { event }), usage: ledger => notify('usage', { ledger }),
    fatal: error => { process.send({ type: 'fatal', error: String(error.message ?? error).slice(0, SUMMARY_LIMIT) }); void stopping?.(); },
  };
}

export async function main() {
  const [major, minor] = process.versions.node.split('.').map(Number);
  if (major < 22 || (major === 22 && minor < 19)) throw new Error('Pi requires Node >=22.19');
  if (process.argv[2] === '--version') { console.log(`esf-pi/1 pi-durable/1.0.3 node/${process.versions.node}`); return 0; }
  const request = await readRequest();
  if (process.argv[2] === '--engine') {
    if (!process.send) throw new Error('Pi engine must be owned by its supervisor');
    const channel = engineChannel();
    try { await channel.notify('terminal', { result: await engine(request, channel) }); return 0; }
    catch (error) { await channel.notify('fatal', { error: String(error.message ?? error).slice(0, SUMMARY_LIMIT) }); return 1; }
    finally { process.disconnect(); }
  }
  if (process.argv.length !== 2) throw new Error('Pi invocation is fixed');
  return supervise(request, fileURLToPath(import.meta.url));
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  main().then(code => { process.exitCode = code; }).catch(error => { console.error(`pi: ${error.message}`); process.exitCode = 1; });
}
