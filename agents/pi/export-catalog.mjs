// Export the published, locked 1.0.3 catalog for pre-allocation Go validation.
// No network refresh occurs at configuration or execution time.
import { MODELS } from './node_modules/@earendil-works/pi-ai/dist/models.generated.js';
import { getSupportedThinkingLevels } from '@earendil-works/pi-ai/models';
import * as fs from 'node:fs/promises';

const catalog = {};
for (const [provider, entries] of Object.entries(MODELS)) {
  for (const model of Object.values(entries)) {
    if (!['openai-completions', 'openai-responses'].includes(model.api) || !model.input.includes('text')) continue;
    // Tiered prices require a richer ESF metadata contract; usage remains
    // measurable, but v1 does not advertise a complete cost estimate for them.
    const pricingKnown = !model.cost.tiers?.length && Object.values(model.cost).some(v => typeof v === 'number' && v > 0);
    const definition = { id: model.id, name: model.name, api: model.api, reasoning: model.reasoning,
      input: ['text'], contextWindow: model.contextWindow, maxTokens: model.maxTokens,
      pricingKnown, cost: { input: model.cost.input, output: model.cost.output, cacheRead: model.cost.cacheRead, cacheWrite: model.cost.cacheWrite },
      thinkingLevels: getSupportedThinkingLevels(model), thinkingLevelMap: model.thinkingLevelMap, compat: model.compat };
    (catalog[provider] ??= {})[model.id] = definition;
  }
}
await fs.writeFile(process.argv[2] ?? '../../internal/agentharness/pi-catalog.json', JSON.stringify(catalog, null, 2) + '\n');
