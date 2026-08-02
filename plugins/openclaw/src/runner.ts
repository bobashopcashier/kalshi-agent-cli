import { execFile } from "node:child_process";

type ProcessError = Error & {
  code?: number | string;
  killed?: boolean;
  signal?: string;
};

type ProcessOutcome = {
  error: ProcessError | null;
  stdout: string;
  stderr: string;
};

export type RunOptions = {
  command: string;
  timeoutMs: number;
  maxBuffer: number;
  signal?: AbortSignal;
};

function pluginError(
  command: string,
  code: string,
  message: string,
  processError?: ProcessError | null,
): Record<string, unknown> {
  return {
    schema_version: "kalshi.openclaw/v1",
    output_contract_version: "kalshi.openclaw/kalshi_query/v1",
    ok: false,
    command,
    effect: {
      class: "local",
      network: false,
      mutation: false,
      potential_mutation: false,
    },
    error: {
      code,
      message,
      retryable: false,
    },
    meta: {
      plugin: "@bobashopcashier/kalshi-cli",
      exit_code: typeof processError?.code === "number" ? processError.code : null,
      signal: processError?.signal ?? null,
    },
  };
}
function parseJsonObject(text: string): Record<string, unknown> | null {
  const trimmed = text.trim();
  if (!trimmed) {
    return null;
  }
  try {
    const value: unknown = JSON.parse(trimmed);
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      return value as Record<string, unknown>;
    }
  } catch {
    return null;
  }
  return null;
}

export function processOutcomeToEnvelope(
  outcome: ProcessOutcome,
  command: string,
): Record<string, unknown> {
  const parsed = parseJsonObject(outcome.stdout) ?? parseJsonObject(outcome.stderr);
  if (parsed) {
    return parsed;
  }

  if (outcome.error?.code === "ENOENT") {
    return pluginError(
      command,
      "CLI_NOT_FOUND",
      "The kalshi executable was not found. Install it or configure binaryPath.",
      outcome.error,
    );
  }
  if (outcome.error?.killed || outcome.error?.signal) {
    return pluginError(command, "CLI_TIMEOUT", "The kalshi process did not finish in time.", outcome.error);
  }
  return pluginError(
    command,
    "INVALID_CLI_OUTPUT",
    "The kalshi process did not return a JSON object.",
    outcome.error,
  );
}

export async function runKalshi(
  binaryPath: string,
  argv: string[],
  options: RunOptions,
): Promise<Record<string, unknown>> {
  const outcome = await new Promise<ProcessOutcome>((resolve) => {
    execFile(
      binaryPath,
      argv,
      {
        encoding: "utf8",
        maxBuffer: options.maxBuffer,
        timeout: options.timeoutMs,
        signal: options.signal,
        windowsHide: true,
      },
      (error, stdout, stderr) => {
        resolve({
          error: error as ProcessError | null,
          stdout: String(stdout ?? ""),
          stderr: String(stderr ?? ""),
        });
      },
    );
  });

  return processOutcomeToEnvelope(outcome, options.command);
}
