/**
 * Operator Extension — loaded into every agent pod.
 *
 * Reads config from /etc/operator/config.json and:
 * 1. Loads OCI/ConfigMap/inline tools from /tools/
 * 2. Registers run_agent + get_agent_run tools
 * 3. Registers beforeToolCall/afterToolCall security hooks via Pi events
 * 4. Emits telemetry events
 *
 * NOTE: MCP is handled at the sidecar level by the operator,
 * not by the runtime directly. MCP gateway sidecars expose tools
 * via Pi's built-in tool system or future MCP integration.
 */

import { existsSync } from "node:fs";
import { Type } from "@sinclair/typebox";
import type { AgentSession, ModelRegistry } from "@mariozechner/pi-coding-agent";
import type { OperatorConfig } from "./types.js";
import { K8sClient } from "./k8s-client.js";

/**
 * Load the operator extension into a Pi session.
 */
export async function loadOperatorExtension(
  session: AgentSession,
  config: OperatorConfig,
  modelRegistry: ModelRegistry,
): Promise<void> {
  const k8sClient = new K8sClient();

  // 1. Load tools from /tools/ directories
  for (const tool of config.tools ?? []) {
    try {
      const indexPath = `${tool.path}/index.js`;
      if (existsSync(indexPath)) {
        const mod = await import(indexPath);
        if (mod.tools && Array.isArray(mod.tools)) {
          for (const t of mod.tools) {
            // Tools loaded from OCI/ConfigMap must conform to Pi ToolDefinition
            session.agent.state.tools = [...session.agent.state.tools, t];
          }
          console.log(
            `[extension] Loaded ${mod.tools.length} tools from ${tool.name}`,
          );
        }
      }
    } catch (err) {
      console.error(`[extension] Failed to load tool ${tool.name}:`, err);
    }
  }

  // 2. Register run_agent tool (agentic orchestration via K8s)
  session.agent.state.tools = [
    ...session.agent.state.tools,
    {
      name: "run_agent",
      label: "Run Agent",
      description:
        "Trigger another agent with a prompt. Creates an AgentRun CR tracked by the operator.",
      parameters: Type.Object({
        agent: Type.String({ description: "Agent name" }),
        prompt: Type.String({ description: "Prompt to send" }),
      }),
      async execute(
        _toolCallId: string,
        params: { agent: string; prompt: string },
      ) {
        const run = await k8sClient.createAgentRun({
          agentRef: params.agent,
          prompt: params.prompt,
          source: "agent",
          sourceRef: process.env.AGENT_NAME ?? "unknown",
        });
        return {
          content: [{ type: "text" as const, text: `AgentRun ${run.name} created` }],
          details: { name: run.name },
        };
      },
    },
  ];

  // 3. Register get_agent_run tool
  session.agent.state.tools = [
    ...session.agent.state.tools,
    {
      name: "get_agent_run",
      label: "Get Agent Run",
      description: "Check the status and output of an AgentRun.",
      parameters: Type.Object({
        name: Type.String({ description: "AgentRun name" }),
      }),
      async execute(
        _toolCallId: string,
        params: { name: string },
      ) {
        const run = await k8sClient.getAgentRun(params.name);
        return {
          content: [
            {
              type: "text" as const,
              text: `Phase: ${run.status?.phase ?? "Unknown"}\nOutput: ${run.status?.output ?? "(pending)"}`,
            },
          ],
          details: {
            phase: run.status?.phase,
            output: run.status?.output,
          },
        };
      },
    },
  ];

  // 4. beforeToolCall / afterToolCall hooks — defense-in-depth
  if (config.toolHooks) {
    session.agent.beforeToolCall = async (context) => {
      const { toolCall, args } = context;
      const toolName = toolCall.name;

      // Block dangerous patterns in bash commands
      if (
        toolName === "bash" &&
        config.toolHooks?.blockedCommands?.length
      ) {
        const cmd =
          (args as { command?: string })?.command ?? "";
        for (const blocked of config.toolHooks.blockedCommands) {
          if (cmd.includes(blocked)) {
            return { block: true, reason: `Blocked command pattern: ${blocked}` };
          }
        }
      }

      // Path-based access control
      if (config.toolHooks?.allowedPaths?.length) {
        const params = args as { path?: string; filePath?: string };
        const path = params.path ?? params.filePath ?? "";
        if (
          path &&
          !config.toolHooks.allowedPaths.some((p: string) =>
            path.startsWith(p),
          )
        ) {
          return { block: true, reason: `Path not in allowed list: ${path}` };
        }
      }

      return undefined;
    };

    session.agent.afterToolCall = async (context) => {
      const { toolCall } = context;

      // Audit logging for sensitive tools
      if (config.toolHooks?.auditTools?.includes(toolCall.name)) {
        console.log(
          JSON.stringify({
            type: "audit",
            tool: toolCall.name,
            timestamp: Date.now(),
          }),
        );
      }

      return undefined;
    };
  }

  // 5. Telemetry via event subscription
  session.agent.subscribe((event) => {
    if (event.type === "tool_execution_end") {
      console.log(
        JSON.stringify({
          type: "telemetry:tool",
          tool: event.toolName,
          success: !event.isError,
          ts: Date.now(),
        }),
      );
    }
  });

  console.log("[extension] Operator extension loaded");
}
