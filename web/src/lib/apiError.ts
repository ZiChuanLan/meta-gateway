/**
 * Where an error came from, so the console can stop guessing from the status
 * alone. A 429 answered by the gateway's own admin API is the console being
 * throttled; the same status relayed from an upstream channel is that channel
 * throttling the model. Only `gateway_admin` is produced today (the console's
 * own requests); the other values are the contract for future attribution.
 */
export type ApiErrorOrigin = "gateway_admin" | "gateway_relay" | "upstream" | "client";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly retryAfter?: number,
    public readonly origin?: ApiErrorOrigin,
  ) {
    super(message);
    this.name = "ApiError";
  }
}
