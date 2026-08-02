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
export declare function processOutcomeToEnvelope(outcome: ProcessOutcome, command: string): Record<string, unknown>;
export declare function runKalshi(binaryPath: string, argv: string[], options: RunOptions): Promise<Record<string, unknown>>;
export {};
