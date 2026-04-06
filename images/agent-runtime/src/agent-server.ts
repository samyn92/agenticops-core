/**
 * Agent Runtime — Daemon Mode (agent-server.ts)
 *
 * HTTP server on :4096 wrapping Pi SDK via createAgentSession().
 * Endpoints: /prompt, /prompt/stream, /steer, /followup, /abort, /healthz, /status
 *
 * Loads operator extension (tools, hooks, telemetry). Registers all providers
 * via AuthStorage. Configures fallback models.
 */

import { readFileSync } from "node:fs";
import express from "express";
import {
  createAgentSession,
  SessionManager,
  AuthStorage,
  ModelRegistry,
  SettingsManager,
} from "@mariozechner/pi-coding-agent";
import type { AgentSession } from "@mariozechner/pi-coding-agent";
import type { Model, Api, ThinkingLevel } from "@mariozechner/pi-ai";
import type {
  OperatorConfig,
  AgentStatus,
  PromptRequest,
  SteerRequest,
  FollowupRequest,
} from "./types.js";
import { loadOperatorExtension } from "./operator-extension.js";

const PORT = 4096;
const CONFIG_PATH = "/etc/operator/config.json";

async function main() {
  // Load operator config
  const config: OperatorConfig = JSON.parse(
    readFileSync(CONFIG_PATH, "utf-8"),
  );

  console.log(`[agent-server] Starting daemon agent`);
  console.log(`[agent-server] Primary model: ${config.primaryProvider}/${config.primaryModel}`);
  console.log(`[agent-server] Providers: ${config.providers.map((p) => p.name).join(", ")}`);

  // Setup Pi SDK components
  const authStorage = AuthStorage.inMemory();
  const modelRegistry = ModelRegistry.create(authStorage);

  // Register provider API keys from environment
  for (const provider of config.providers) {
    const envKey = `${provider.name.toUpperCase()}_API_KEY`;
    const apiKey = process.env[envKey];
    if (apiKey) {
      authStorage.setRuntimeApiKey(provider.name, apiKey);
      console.log(`[agent-server] Registered provider: ${provider.name}`);
    } else {
      console.warn(
        `[agent-server] WARNING: No API key found for provider ${provider.name} (env: ${envKey})`,
      );
    }
  }

  // Configure settings (compaction, thinking level)
  const settingsManager = SettingsManager.inMemory({
    compaction: {
      enabled: config.compaction?.enabled ?? true,
    },
    defaultThinkingLevel: (config.thinkingLevel ?? "off") as ThinkingLevel,
  });

  // Resolve primary model
  const primaryModel = modelRegistry.find(config.primaryProvider, config.primaryModel);
  if (!primaryModel) {
    console.error(
      `[agent-server] FATAL: Model ${config.primaryProvider}/${config.primaryModel} not found in registry`,
    );
    process.exit(1);
  }

  // Create session via Pi SDK
  const sessionManager = SessionManager.create("/data/sessions");
  const { session } = await createAgentSession({
    sessionManager,
    authStorage,
    modelRegistry,
    settingsManager,
    model: primaryModel,
  });

  // Load operator extension (tools, hooks, telemetry)
  await loadOperatorExtension(session, config, modelRegistry);

  // Runtime state tracking
  let busy = false;
  let lastOutput = "";
  let totalToolCalls = 0;
  let totalTokensUsed = 0;
  let totalCost = 0;
  let activeModel = `${config.primaryProvider}/${config.primaryModel}`;

  // Subscribe to agent events for tracking
  session.agent.subscribe((event) => {
    if (event.type === "message_end") {
      // Extract text content from assistant messages
      const msg = event.message;
      if (msg && "role" in msg && msg.role === "assistant" && "content" in msg) {
        const textParts = (msg.content as Array<{ type: string; text?: string }>)
          .filter((c) => c.type === "text")
          .map((c) => c.text ?? "");
        lastOutput = textParts.join("");
      }
      // Track usage from response metadata
      if (msg && "usage" in msg) {
        const usage = msg.usage as { totalTokens?: number; cost?: { total?: number } } | undefined;
        if (usage) {
          totalTokensUsed += usage.totalTokens ?? 0;
          totalCost += usage.cost?.total ?? 0;
        }
      }
    }
    if (event.type === "tool_execution_end") {
      totalToolCalls++;
    }
  });

  // Resolve a model string like "anthropic/claude-sonnet-4-..." to a Model object
  function resolveModel(provider: string, modelId: string): Model<Api> | undefined {
    return modelRegistry.find(provider, modelId);
  }

  // Prompt handler with fallback model support
  async function handlePrompt(prompt: string): Promise<void> {
    const models: Array<{ provider: string; model: string }> = [
      { provider: config.primaryProvider, model: config.primaryModel },
      ...(config.fallbackModels ?? []),
    ];

    for (const entry of models) {
      try {
        const model = resolveModel(entry.provider, entry.model);
        if (!model) {
          console.warn(`[agent-server] Model ${entry.provider}/${entry.model} not found, skipping`);
          continue;
        }
        await session.setModel(model);
        activeModel = `${entry.provider}/${entry.model}`;
        await session.prompt(prompt);
        return;
      } catch (err: unknown) {
        const status = (err as { status?: number })?.status;
        const isRetryable = status === 429 || (status !== undefined && status >= 500);
        const isLast = entry === models[models.length - 1];
        if (isRetryable && !isLast) {
          console.warn(`[agent-server] Model ${entry.provider}/${entry.model} failed, trying fallback...`);
          continue;
        }
        throw err;
      }
    }

    throw new Error("All models failed");
  }

  // Express HTTP server
  const app = express();
  app.use(express.json());

  // POST /prompt — send prompt, returns on completion
  app.post("/prompt", async (req, res) => {
    const { prompt } = req.body as PromptRequest;
    if (!prompt) {
      res.status(400).json({ error: "prompt is required" });
      return;
    }

    if (busy) {
      res.status(429).json({ error: "Agent is busy" });
      return;
    }

    busy = true;
    try {
      await handlePrompt(prompt);
      res.json({ output: lastOutput, model: activeModel });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      res.status(500).json({ error: message });
    } finally {
      busy = false;
    }
  });

  // POST /prompt/stream — SSE event stream
  app.post("/prompt/stream", async (req, res) => {
    const { prompt } = req.body as PromptRequest;
    if (!prompt) {
      res.status(400).json({ error: "prompt is required" });
      return;
    }

    if (busy) {
      res.status(429).json({ error: "Agent is busy" });
      return;
    }

    res.setHeader("Content-Type", "text/event-stream");
    res.setHeader("Cache-Control", "no-cache");
    res.setHeader("Connection", "keep-alive");

    busy = true;

    // Subscribe to streaming events for this request
    const unsubscribe = session.agent.subscribe((event) => {
      if (event.type === "message_update" && event.assistantMessageEvent) {
        const sse = event.assistantMessageEvent;
        if ("type" in sse && (sse.type === "text_delta" || sse.type === "thinking_delta")) {
          const delta = "delta" in sse ? sse.delta : "";
          res.write(`data: ${JSON.stringify({ type: sse.type, delta })}\n\n`);
        }
      }
      if (event.type === "tool_execution_start") {
        res.write(
          `data: ${JSON.stringify({ type: "tool_start", tool: event.toolName })}\n\n`,
        );
      }
      if (event.type === "tool_execution_end") {
        res.write(
          `data: ${JSON.stringify({ type: "tool_end", tool: event.toolName, error: event.isError })}\n\n`,
        );
      }
    });

    try {
      await handlePrompt(prompt);
      res.write(`data: ${JSON.stringify({ type: "complete", output: lastOutput, model: activeModel })}\n\n`);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      res.write(`data: ${JSON.stringify({ type: "error", error: message })}\n\n`);
    } finally {
      unsubscribe();
      busy = false;
      res.end();
    }
  });

  // POST /steer — redirect agent mid-conversation
  app.post("/steer", async (req, res) => {
    const { message } = req.body as SteerRequest;
    if (!message) {
      res.status(400).json({ error: "message is required" });
      return;
    }

    try {
      await session.steer(message);
      res.json({ ok: true });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      res.status(500).json({ error: message });
    }
  });

  // POST /followup — continue in existing session
  app.post("/followup", async (req, res) => {
    const { prompt } = req.body as FollowupRequest;
    if (!prompt) {
      res.status(400).json({ error: "prompt is required" });
      return;
    }

    if (busy) {
      res.status(429).json({ error: "Agent is busy" });
      return;
    }

    busy = true;
    try {
      await session.followUp(prompt);
      res.json({ output: lastOutput, model: activeModel });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      res.status(500).json({ error: message });
    } finally {
      busy = false;
    }
  });

  // DELETE /abort — cancel current execution
  app.delete("/abort", async (_req, res) => {
    try {
      await session.abort();
      busy = false;
      res.json({ ok: true });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      res.status(500).json({ error: message });
    }
  });

  // GET /healthz — health check
  app.get("/healthz", (_req, res) => {
    res.json({ status: "ok" });
  });

  // GET /status — session stats
  app.get("/status", (_req, res) => {
    const status: AgentStatus = {
      busy,
      output: lastOutput,
      toolCalls: totalToolCalls,
      tokensUsed: totalTokensUsed,
      cost: totalCost.toFixed(4),
      model: activeModel,
      sessionId: session.sessionId,
    };
    res.json(status);
  });

  app.listen(PORT, () => {
    console.log(`[agent-server] Listening on :${PORT}`);
  });
}

main().catch((err) => {
  console.error("[agent-server] Fatal error:", err);
  process.exit(1);
});
