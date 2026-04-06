/**
 * Agent Runtime — Task Mode (agent-task.ts)
 *
 * Reads AGENT_PROMPT env var, runs the prompt through Pi SDK,
 * writes a structured JSON result to stdout, then exits.
 * Uses SessionManager.inMemory() — nothing persists.
 */

import { readFileSync } from "node:fs";
import {
  createAgentSession,
  SessionManager,
  AuthStorage,
  ModelRegistry,
} from "@mariozechner/pi-coding-agent";
import type { Model } from "@mariozechner/pi-ai";
import type { OperatorConfig, TaskResult } from "./types.js";
import { loadOperatorExtension } from "./operator-extension.js";

const CONFIG_PATH = "/etc/operator/config.json";

async function main() {
  const prompt = process.env.AGENT_PROMPT;
  if (!prompt) {
    const result: TaskResult = {
      output: "",
      toolCalls: 0,
      tokensUsed: 0,
      cost: 0,
      model: "",
      success: false,
      error: "AGENT_PROMPT environment variable is not set",
    };
    process.stdout.write(JSON.stringify(result) + "\n");
    process.exit(1);
  }

  // Load operator config
  const config: OperatorConfig = JSON.parse(
    readFileSync(CONFIG_PATH, "utf-8"),
  );

  console.error(`[agent-task] Running task with model: ${config.primaryProvider}/${config.primaryModel}`);
  console.error(`[agent-task] Prompt: ${prompt.substring(0, 100)}...`);

  // Setup Pi SDK (in-memory, no persistence)
  const authStorage = AuthStorage.inMemory();
  const modelRegistry = ModelRegistry.create(authStorage);

  // Register provider API keys from environment
  for (const provider of config.providers) {
    const envKey = `${provider.name.toUpperCase()}_API_KEY`;
    const apiKey = process.env[envKey];
    if (apiKey) {
      authStorage.setRuntimeApiKey(provider.name, apiKey);
    }
  }

  // Resolve primary model
  const primaryModel = modelRegistry.find(config.primaryProvider, config.primaryModel);
  if (!primaryModel) {
    const result: TaskResult = {
      output: "",
      toolCalls: 0,
      tokensUsed: 0,
      cost: 0,
      model: `${config.primaryProvider}/${config.primaryModel}`,
      success: false,
      error: `Model ${config.primaryProvider}/${config.primaryModel} not found in registry`,
    };
    process.stdout.write(JSON.stringify(result) + "\n");
    process.exit(1);
  }

  const { session } = await createAgentSession({
    sessionManager: SessionManager.inMemory(),
    authStorage,
    modelRegistry,
    model: primaryModel,
  });

  // Load operator extension (tools, hooks)
  await loadOperatorExtension(session, config, modelRegistry);

  // Track output and metrics via event subscription
  let output = "";
  let toolCalls = 0;
  let tokensUsed = 0;
  let cost = 0;

  session.agent.subscribe((event) => {
    if (event.type === "message_end") {
      const msg = event.message;
      if (msg && "role" in msg && msg.role === "assistant" && "content" in msg) {
        const textParts = (msg.content as Array<{ type: string; text?: string }>)
          .filter((c) => c.type === "text")
          .map((c) => c.text ?? "");
        output = textParts.join("");
      }
      if (msg && "usage" in msg) {
        const usage = msg.usage as { totalTokens?: number; cost?: { total?: number } } | undefined;
        if (usage) {
          tokensUsed += usage.totalTokens ?? 0;
          cost += usage.cost?.total ?? 0;
        }
      }
    }
    if (event.type === "tool_execution_end") {
      toolCalls++;
    }
  });

  // Execute prompt with fallback
  const models: Array<{ provider: string; model: string }> = [
    { provider: config.primaryProvider, model: config.primaryModel },
    ...(config.fallbackModels ?? []),
  ];
  let lastError: Error | null = null;
  let usedModel = `${config.primaryProvider}/${config.primaryModel}`;

  for (const entry of models) {
    try {
      const model = modelRegistry.find(entry.provider, entry.model);
      if (!model) {
        console.error(`[agent-task] Model ${entry.provider}/${entry.model} not found, skipping`);
        continue;
      }
      await session.setModel(model);
      usedModel = `${entry.provider}/${entry.model}`;
      await session.prompt(prompt);

      // Success
      const result: TaskResult = {
        output,
        toolCalls,
        tokensUsed,
        cost,
        model: usedModel,
        success: true,
      };
      process.stdout.write(JSON.stringify(result) + "\n");
      process.exit(0);
    } catch (err: unknown) {
      lastError = err instanceof Error ? err : new Error(String(err));
      const status = (err as { status?: number })?.status;
      const isRetryable = status === 429 || (status !== undefined && status >= 500);
      const isLast = entry === models[models.length - 1];
      if (isRetryable && !isLast) {
        console.error(`[agent-task] Model ${entry.provider}/${entry.model} failed, trying fallback...`);
        continue;
      }
    }
  }

  // All models failed
  const result: TaskResult = {
    output: "",
    toolCalls: 0,
    tokensUsed: 0,
    cost: 0,
    model: usedModel,
    success: false,
    error: lastError?.message ?? "Unknown error",
  };

  process.stdout.write(JSON.stringify(result) + "\n");
  process.exit(1);
}

main().catch((err) => {
  console.error("[agent-task] Fatal error:", err);
  process.exit(1);
});
