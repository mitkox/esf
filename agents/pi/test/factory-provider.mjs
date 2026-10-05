// Test-only OpenAI-compatible provider. Start exclusively inside a Cube VM.
import { createServer } from 'node:http';
createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/v1/models') {
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ object: 'list', data: [{ id: 'test-model', context_window: 32768, max_tokens: 1024 }] })); return;
  }
  if (req.method !== 'POST' || req.url !== '/v1/chat/completions') { res.writeHead(404); res.end(); return; }
  const chunks = []; for await (const chunk of req) chunks.push(chunk);
  const body = JSON.parse(Buffer.concat(chunks));
  const prompt = JSON.stringify(body.messages.filter(m => m.role === 'user'));
  const finished = body.messages.some(m => m.role === 'tool');
  const write = body.tools.find(t => t.function.name === 'write');
  const args = prompt.includes('PI_ACCEPTANCE_BLOCKED')
    ? { path: '../.factory/blocked.json', content: JSON.stringify({ reason: 'deterministic acceptance blocked task' }) }
    : { path: 'hello.txt', content: prompt.includes('PI_ACCEPTANCE_FAIL') ? 'wrong\n' : 'hello pi\n' };
  const delta = finished ? { content: 'Completed the deterministic acceptance task.' }
    : { tool_calls: [{ index: 0, id: 'write_1', type: 'function', function: { name: write.function.name, arguments: JSON.stringify(args) } }] };
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const chunk = (delta, finish_reason = null) => ({ id: 'fixture-chat', object: 'chat.completion.chunk', created: 1, model: 'test-model', choices: [{ index: 0, delta, finish_reason }] });
  res.write(`data: ${JSON.stringify(chunk({ role: 'assistant', ...delta }))}\n\n`);
  res.write(`data: ${JSON.stringify(chunk({}, finished ? 'stop' : 'tool_calls'))}\n\n`);
  res.write(`data: ${JSON.stringify({ ...chunk({}), choices: [], usage: { prompt_tokens: 100, completion_tokens: 10 } })}\n\n`);
  res.end('data: [DONE]\n\n');
}).listen(18567, '127.0.0.1');
