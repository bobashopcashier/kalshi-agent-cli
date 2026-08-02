import { Type } from "typebox";
import { defineToolPlugin } from "openclaw/plugin-sdk/tool-plugin";
import { ALLOWED_COMMANDS, buildKalshiArgs } from "./commands.js";
import { runKalshi } from "./runner.js";
const commandSchema = Type.Union(ALLOWED_COMMANDS.map((command) => Type.Literal(command)));
const fieldPath = Type.String({ minLength: 1, maxLength: 256 });
export default defineToolPlugin({
    id: "kalshi-cli",
    name: "Kalshi CLI",
    description: "Stable, bounded Kalshi tools for OpenClaw agents.",
    configSchema: Type.Object({
        binaryPath: Type.Optional(Type.String({
            minLength: 1,
            description: "Path to the kalshi executable. Defaults to kalshi on PATH.",
        })),
    }, { additionalProperties: false }),
    tools: (tool) => [
        tool({
            name: "kalshi_query",
            label: "Kalshi Query",
            description: "Run an allowlisted, non-mutating Kalshi CLI command with bounded JSON output. Use commands.list and commands.describe for offline discovery. Returns the CLI's versioned envelope, including UPSTREAM_SCHEMA_MISMATCH field paths.",
            optional: true,
            parameters: Type.Object({
                command: commandSchema,
                targetCommand: Type.Optional(Type.String({
                    pattern: "^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$",
                    description: "Command to inspect. Valid only when command is commands.describe.",
                })),
                params: Type.Optional(Type.Record(Type.String(), Type.Unknown(), {
                    description: "Strict command parameters passed as one --params JSON object.",
                })),
                fields: Type.Optional(Type.Array(fieldPath, { maxItems: 128 })),
                requireFields: Type.Optional(Type.Array(fieldPath, { maxItems: 128 })),
                environment: Type.Optional(Type.Union([Type.Literal("demo"), Type.Literal("production")])),
                authenticated: Type.Optional(Type.Boolean()),
                maxPages: Type.Optional(Type.Integer({ minimum: 1, maximum: 10 })),
                maxItems: Type.Optional(Type.Integer({ minimum: 1, maximum: 1000 })),
                maxBytes: Type.Optional(Type.Integer({ minimum: 1024, maximum: 1_048_576 })),
                timeoutSeconds: Type.Optional(Type.Integer({ minimum: 1, maximum: 60 })),
            }, { additionalProperties: false }),
            async execute(params, config, context) {
                context.signal?.throwIfAborted();
                const input = params;
                const argv = buildKalshiArgs(input);
                const timeoutSeconds = input.timeoutSeconds ?? 15;
                const maxBytes = input.maxBytes ?? 262_144;
                return runKalshi(config.binaryPath?.trim() || "kalshi", argv, {
                    command: input.command,
                    timeoutMs: timeoutSeconds * 1000 + 1000,
                    maxBuffer: Math.min(maxBytes + 131_072, 1_179_648),
                    signal: context.signal,
                });
            },
        }),
    ],
});
//# sourceMappingURL=index.js.map