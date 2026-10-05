import path from 'node:path';
import { BACKGROUND_CONTEXT, withAbortSignal } from '@earendil-works/chord/context';
import { createModels, createProvider } from '@earendil-works/pi-ai/models';
import { openAICompletionsApi } from '@earendil-works/pi-ai/api/openai-completions.lazy';
import { openAIResponsesApi } from '@earendil-works/pi-ai/api/openai-responses.lazy';
import { AssistantMessageEventStream } from '@earendil-works/pi-ai/utils/event-stream';
import { createRegistry, defineExtension, Harness, section, watchEvents } from '@earendil-works/pi-durable';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { createReadTool, createWriteTool, createEditTool, createBashTool } from '@earendil-works/pi-durable/tools';
import { openNodeJsonlStorage } from '@earendil-works/pi-durable/storage/jsonl/node';
import { validateRequest } from './protocol.mjs';

const instructions = `You are a coding agent inside one disposable ESF CubeSandbox microVM.
Use the supplied repository and task. Read AGENTS.md and the factory inventory before editing.
Do not alter factory metadata, verification commands, or installed agent/runtime files.
The factory independently verifies your changes. Your final answer is not verification evidence.
If blocked, write ../.factory/blocked.json beside the repository with a nonempty reason and optional detail, then stop.
An interrupted tool may have partially changed files. Inspect the current tree before making more changes.
Do not create background/daemon processes or access destinations outside operator policy.`;

export async function engine(request, channel) {
  const r = validateRequest(request);
  const abort = new AbortController();
  const context = withAbortSignal(abort.signal, BACKGROUND_CONTEXT);
  let harness, root, events;
  channel.onStop(async () => {
    abort.abort();
    if (root) await root.abort(BACKGROUND_CONTEXT).catch(() => {});
  });
  const models = createModels();
  const api = r.api === 'openai-responses' ? openAIResponsesApi() : openAICompletionsApi();
  // Every provider attempt (including compaction) is observed. Zero usage can
  // mean missing provider metering; never turn that into a zero-cost invoice.
  const tracked = method => (model, messages, options) => {
    const outer = new AssistantMessageEventStream();
    (async () => {
      const source = api[method](model, messages, options);
      for await (const event of source) {
        if (event.type === 'done' || event.type === 'error') {
          const message = event.message ?? event.error;
          if (!message?.usage?.totalTokens || event.type === 'error') await channel.notify('usage-incomplete');
        }
        outer.push(event);
      }
      outer.end(await source.result());
    })().catch(error => { outer.end(); channel.fatal(error); });
    return outer;
  };
  const metadata = r.metadata;
  models.setProvider(createProvider({
    id: r.provider, name: r.provider, baseUrl: r.base_url,
    auth: { apiKey: { name: 'ESF operator model connection', resolve: async () => ({ auth: { apiKey: process.env.ESF_PI_API_KEY ?? 'cube-egress-managed-placeholder' }, source: 'ESF' }) } },
    models: [{ id: r.model, name: metadata.name, provider: r.provider, api: r.api, baseUrl: r.base_url,
      reasoning: metadata.reasoning, input: metadata.input, contextWindow: metadata.contextWindow,
      maxTokens: metadata.maxTokens, thinkingLevelMap: metadata.thinkingLevelMap, compat: metadata.compat,
      cost: metadata.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
    api: { [r.api]: { stream: tracked('stream'), streamSimple: tracked('streamSimple') } },
  }));
  const bash = createBashTool();
  const guardedBash = { ...bash, replay: 'unsafe', execute: async (args, api, ctx) => {
    // Wait for the supervisor acknowledgement before the tool can spawn.
    await channel.notify('shell-start', { call_id: api.callId });
    try { return await bash.execute(args, api, ctx); }
    finally { await channel.notify('shell-end', { call_id: api.callId }); }
  } };
  const registry = createRegistry();
  registry.install(defineExtension({ name: 'esf-coding', sections: [section('esf', () => instructions, { tag: false })],
    tools: [createReadTool(), createWriteTool(), createEditTool(), guardedBash].map(tool => ({ ...tool, replay: 'unsafe' })) }));
  try {
    const storage = await openNodeJsonlStorage(path.join(r.state_dir, 'journal'), context, { fsync: true });
    harness = await Harness.open(storage, { models, registry,
      env: target => new NodeExecutionEnv({ cwd: target.cwd ?? r.repository_dir }),
      settings: { toolExecution: 'sequential', retry: { enabled: false },
        stream: { maxRetries: 0, maxTokens: metadata.maxTokens },
        compaction: { enabled: true, reserveTokens: metadata.maxTokens,
          keepRecentTokens: Math.min(20_000, Math.floor(metadata.contextWindow / 4)), backgroundTokens: 0 } },
    }, context);
    root = await harness.root(context, { agent: { model: { provider: r.provider, modelId: r.model },
      thinkingLevel: r.thinking_level, cwd: r.repository_dir } });
    events = await watchEvents(harness, root.id, context);
    await channel.audit(events.snapshot);
    await channel.usage(events.snapshot.usage);
    events.start(async batch => {
      for (const event of batch) {
        await channel.audit(event);
        if (event.type === 'usage_changed' || event.type === 'snapshot') await channel.usage(event.usage);
      }
    });
    // submit() is idempotent by requestId. It also starts recovered pending
    // work, so a committed answer is returned without another model request.
    const submission = await root.submit({ type: 'input', content: r.prompt, requestId: r.request_id }, context);
    const settled = await submission.wait(context);
    // wait() returns the committed record. Watch shutdown can discard queued
    // events, so explicitly acknowledge this final durable boundary.
    await channel.audit({ type: 'submission', record: settled });
    const ledger = await harness.usage(context);
    await events.stop(); events = undefined;
    await harness.close(BACKGROUND_CONTEXT); harness = undefined;
    return { status: settled.status === 'done' ? 'completed' : 'failed',
      error: settled.status === 'done' ? undefined : `Pi submission ${settled.reason ?? settled.status}`, ledger };
  } finally {
    await events?.stop().catch(() => {});
    await harness?.close(BACKGROUND_CONTEXT).catch(() => {});
  }
}
