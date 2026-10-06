import type { api } from "../../api/client";
import type { ModelCapability } from "../../api/types";
import { primaryChannelName, upstreamChoices, type Translate } from "../models/routingPolicy";
import { accountRequest } from "../../team/transport";
import type { UserKey, UserModel } from "../../team/types";

/**
 * What the workbench UI needs from whoever is asking the gateway.
 *
 * The two roles run genuinely different machines: staff probe through
 * /admin/try/* with the deployment bearer, which bypasses downstream tokens (and
 * with them every quota and its billing); a member probes with a token they can
 * reveal, through the real /v1, so metering, quota, failover and the request log
 * behave exactly as they will for their own code. That difference is real and
 * stays.
 *
 * The UI around it does not have to differ — and it used to: the member app
 * carried a second, simpler workbench (one turn, no streaming, its own result
 * markup), so the same page in the same console looked and behaved like a
 * different product depending on who opened it. This is the seam that lets one
 * set of components — the threaded playground, the image studio with its history
 * and reference-image handling — sit on top of either machine.
 */
export type WorkbenchModel = {
  name: string;
  /** The /v1 endpoints this model answers, e.g. ["/v1/chat/completions"]. */
  endpoints: string[];
  /**
   * The rows the connection picker offers for this model. Staff pin a route
   * member (channel × upstream model name); a member picks which of their tokens
   * the probe spends, the only thing about the call they control. Same control,
   * and `pickLabelKey` tells it what it is looking at.
   */
  upstreams: { value: number; label: string }[];
  /**
   * The serving connection, when the runner can name it. Staff see it in the
   * image picker's labels ("which site serves this model?"); a member's catalogue
   * has no such fact, so their labels are the model name alone.
   */
  site?: string;
  /**
   * What the image studio needs to know before it sends anything: which kind of
   * model this is, which request shapes the upstream accepts, how many reference
   * images it takes and what sizes it offers. Staff read it from the capability
   * registry; a member's catalogue carries the part the gateway itself knows.
   */
  image?: ImageCapability;
};

export type ImageCapability = {
  kind: string;
  endpoints: string[];
  inputFormats: string[];
  maxInputImages: number;
  sizeOptions: string;
  inputModalities: string[];
  /** The registry's own note about this model's image protocol; "" for a member. */
  notes: string;
};

export type ChatBody = {
  model: string;
  messages?: { role: string; content: string }[];
  system?: string;
  max_tokens?: number;
  temperature?: number;
  top_p?: number;
  /**
   * Admin: the route member to pin. Member: the downstream key to spend. The
   * control is the same one; `pickLabelKey` is what keeps it honest.
   */
  member_id?: number;
};

export type ImageBody = {
  model: string;
  prompt?: string;
  mode?: "generate" | "edit" | "auto";
  format?: "json" | "multipart";
  size?: string;
  n?: number;
  images?: Array<{ data_url: string; name?: string }>;
  member_id?: number;
  include_raw_response?: boolean;
};

export type ImageResult = {
  status: number;
  latency_ms: number;
  model?: string;
  plan?: { endpoint?: string; format?: string };
  images?: Array<{ data_url?: string; url?: string; revised_prompt?: string }>;
  body?: unknown;
  /** The upstream connection that served it, when the runner reports one. */
  channel_name?: string;
};

export type WorkbenchRunner = {
  /** Distinguishes the runners in react-query keys. */
  id: "admin" | "member";
  /** i18n key for the connection picker's label. */
  pickLabelKey: string;
  /** i18n key for its "let the gateway choose" row. */
  autoLabelKey: string;
  /**
   * "Nothing to run" copy, per tab. It differs by role on purpose: staff are told
   * to create a route, because they can; a member can only ask for access, and the
   * admin instruction is advice they cannot act on.
   */
  emptyKeys: { image: string; chat: string };
  /** Everything the model and connection pickers need, in one round trip. */
  catalogue: (signal?: AbortSignal) => Promise<WorkbenchModel[]>;
  chat: (body: ChatBody) => Promise<{
    status: number;
    latency_ms?: number;
    body: unknown;
    channel_name?: string;
  }>;
  /** Raw SSE for one turn; the caller reads `body` and aborts via the signal. */
  streamChat: (body: ChatBody, signal?: AbortSignal) => Promise<Response>;
  image: (body: ImageBody) => Promise<ImageResult>;
};

/** The chat endpoint a model has to answer to appear on the text tab. */
export const CHAT_ENDPOINT = "/v1/chat/completions";

/**
 * Staff runner: the console's own probe endpoints.
 *
 * The model list is the routes (what is callable) resolved against the
 * capability registry (what each name can do) — the derivation the playground
 * used to do inline, kept in one place now that both tabs need it.
 */
export function adminRunner(client: ReturnType<typeof api>, t: Translate): WorkbenchRunner {
  return {
    id: "admin",
    pickLabelKey: "playground.upstream",
    autoLabelKey: "playground.upstreamAuto",
    emptyKeys: { image: "workbench.image.noModels", chat: "playground.noModels" },
    catalogue: async (signal) => {
      const overview = await client.routeOverviews(signal);
      const routes = overview
        .filter(({ route }) => route.enabled && !/[*?]/.test(route.model_pattern))
        .map(({ route }) => route);
      const names = Array.from(new Set(routes.map((route) => route.model_pattern))).sort();
      // A failed capability lookup is an error, not an empty catalogue: the tabs
      // have to say "the registry did not answer" rather than render "no models"
      // as if the gateway had none. A model merely absent from a successful
      // answer keeps its default endpoint (chat), as it did before.
      const items: Record<string, ModelCapability> = names.length
        ? (await client.resolveModelCapabilities(names)).items
        : {};
      return names.map((name) => {
        const found = overview.find(({ route }) => route.model_pattern === name);
        const capability = items[name];
        return {
          name,
          endpoints: capability?.endpoints ?? [CHAT_ENDPOINT],
          upstreams:
            found == null
              ? []
              : upstreamChoices(found.members ?? [], found.route, t).map((choice) => ({
                  value: choice.memberId,
                  label: choice.label,
                })),
          site: found ? primaryChannelName(found) || undefined : undefined,
          image: capability
            ? {
                kind: capability.kind,
                endpoints: capability.endpoints ?? [],
                inputFormats: capability.input_formats ?? [],
                maxInputImages: capability.max_input_images ?? 0,
                sizeOptions: capability.size_options ?? "",
                inputModalities: capability.input_modalities ?? [],
                notes: capability.notes ?? "",
              }
            : undefined,
        };
      });
    },
    chat: (body) => client.tryChat(body),
    streamChat: (body, signal) => client.streamTryChat(body, signal),
    image: (body) => client.tryImage(body),
  };
}

/**
 * Member runner: the member's own token against the real /v1.
 *
 * Everything a member sees here is the call their code will make. The plaintext
 * token is fetched once per request and lives only in that request (a member may
 * reveal their own token — that is what lets the probe be an ordinary /v1 call
 * instead of a gateway-side impersonation of them).
 */
export function memberRunner(): WorkbenchRunner {
  const tokens = new Map<number, string>();
  // The key the "auto" row spends: the first enabled one, remembered from the
  // catalogue so a probe with no explicit pick still has a token to reveal.
  let autoKey = 0;

  async function tokenFor(keyId?: number) {
    const id = keyId && keyId > 0 ? keyId : autoKey;
    if (!id) throw new Error("no usable token");
    const cached = tokens.get(id);
    if (cached) return cached;
    const { token } = await accountRequest<{ token: string }>(`/me/keys/${id}/reveal`, {
      method: "POST",
    });
    tokens.set(id, token);
    return token;
  }

  const headers = (token: string, json: boolean) => ({
    Authorization: `Bearer ${token}`,
    ...(json ? { "Content-Type": "application/json" } : {}),
  });

  return {
    id: "member",
    pickLabelKey: "workbench.pickKey",
    autoLabelKey: "workbench.pickKeyAuto",
    emptyKeys: {
      image: "workbench.image.noModelsMember",
      chat: "playground.noModelsMember",
    },
    catalogue: async (signal) => {
      const [catalogue, keys] = await Promise.all([
        accountRequest<UserModel[]>("/me/model-catalog", { signal }),
        accountRequest<UserKey[]>("/me/keys", { signal }),
      ]);
      // A key is the member's "connection": every probe spends one, so the
      // picker lists the ones that can actually be spent.
      const usable = keys.filter((key) => key.enabled);
      autoKey = usable[0]?.id ?? 0;
      return catalogue.map((model) => {
        // A member catalogue that carries no endpoint information still answers
        // chat, exactly as an unregistered admin model does.
        const declared = (model.endpoints || "")
          .split(",")
          .map((endpoint) => endpoint.trim())
          .filter(Boolean);
        return {
          name: model.name,
          endpoints: declared.length ? declared : [CHAT_ENDPOINT],
          upstreams: usable.map((key) => ({ value: key.id, label: key.name })),
          // The gateway publishes no member-facing capability record, so the
          // studio gets what the catalogue actually knows and no invented
          // limits: 0 reference images means "unbounded", not "none allowed".
          image: {
            kind: model.kind,
            endpoints: declared,
            inputFormats: ["json", "multipart"],
            maxInputImages: 0,
            sizeOptions: "",
            inputModalities: (model.input_modalities || "")
              .split(",")
              .map((value) => value.trim())
              .filter(Boolean),
            notes: "",
          },
        };
      });
    },
    chat: async (body) => {
      const token = await tokenFor(body.member_id);
      const started = performance.now();
      const response = await fetch("/v1/chat/completions", {
        method: "POST",
        headers: headers(token, true),
        body: JSON.stringify({
          model: body.model,
          messages: body.messages,
          ...(body.system ? { system: body.system } : {}),
          ...(body.max_tokens ? { max_tokens: body.max_tokens } : {}),
          ...(body.temperature === undefined ? {} : { temperature: body.temperature }),
          ...(body.top_p === undefined ? {} : { top_p: body.top_p }),
        }),
      });
      const text = await response.text();
      let parsed: unknown = text;
      try {
        parsed = JSON.parse(text);
      } catch {
        // Upstreams do return non-JSON bodies on some failures; the raw text is
        // still the most useful thing to show, so it goes through as the body.
      }
      return {
        status: response.status,
        latency_ms: Math.round(performance.now() - started),
        body: parsed,
      };
    },
    streamChat: async (body, signal) => {
      const token = await tokenFor(body.member_id);
      const response = await fetch("/v1/chat/completions", {
        method: "POST",
        headers: headers(token, true),
        signal,
        body: JSON.stringify({
          model: body.model,
          messages: body.messages,
          stream: true,
          ...(body.system ? { system: body.system } : {}),
          ...(body.max_tokens ? { max_tokens: body.max_tokens } : {}),
          ...(body.temperature === undefined ? {} : { temperature: body.temperature }),
          ...(body.top_p === undefined ? {} : { top_p: body.top_p }),
        }),
      });
      if (response.ok) return response;
      // The playground reads provider frames, so a plain JSON refusal has to
      // arrive as the frame it already understands — otherwise the reader parses
      // an error document as SSE and the turn ends up looking empty.
      const text = await response.text();
      let message = text.slice(0, 400);
      try {
        const parsed = JSON.parse(text) as { error?: { message?: string }; message?: string };
        message = parsed.error?.message || parsed.message || message;
      } catch {
        /* keep the raw text */
      }
      return errorStream(response.status, message);
    },
    image: async (body) => {
      const token = await tokenFor(body.member_id);
      const edits = (body.images ?? []).length > 0;
      const endpoint = edits ? "/v1/images/edits" : "/v1/images/generations";
      const started = performance.now();
      let response: Response;
      if (edits) {
        const form = new FormData();
        form.set("model", body.model);
        if (body.prompt) form.set("prompt", body.prompt);
        if (body.size) form.set("size", body.size);
        if (body.n) form.set("n", String(body.n));
        for (const image of body.images ?? []) form.append("image", dataUrlToFile(image));
        response = await fetch(endpoint, {
          method: "POST",
          headers: headers(token, false),
          body: form,
        });
      } else {
        response = await fetch(endpoint, {
          method: "POST",
          headers: headers(token, true),
          body: JSON.stringify({
            model: body.model,
            prompt: body.prompt,
            ...(body.size ? { size: body.size } : {}),
            ...(body.n ? { n: body.n } : {}),
          }),
        });
      }
      const text = await response.text();
      let payload: {
        data?: Array<{ url?: string; b64_json?: string; revised_prompt?: string }>;
        error?: unknown;
      } | null = null;
      try {
        payload = JSON.parse(text);
      } catch {
        /* non-JSON bodies are shown raw */
      }
      const images = (payload?.data ?? []).map((item) => ({
        url: item.url,
        data_url: item.b64_json ? `data:image/png;base64,${item.b64_json}` : undefined,
        revised_prompt: item.revised_prompt,
      }));
      return {
        status: response.status,
        latency_ms: Math.round(performance.now() - started),
        model: body.model,
        plan: { endpoint, format: edits ? "multipart" : "json" },
        images,
        // Only when the caller asked: the raw document is for the "what did the
        // upstream actually say" view, and it is large.
        body: body.include_raw_response ? (payload ?? text) : payload?.error,
      };
    },
  };
}

/** errorStream renders a refusal as the SSE frame the playground already reads. */
function errorStream(status: number, message: string) {
  const payload = JSON.stringify({ error: { message, status } });
  return new Response(`event: error\ndata: ${payload}\n\nevent: done\ndata: {}\n\n`, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

function dataUrlToFile(image: { data_url: string; name?: string }) {
  const [meta = "", encoded = ""] = image.data_url.split(",");
  const mime = /data:([^;]+)/.exec(meta)?.[1] ?? "image/png";
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
  return new File([bytes], image.name || `reference.${mime.split("/")[1] ?? "png"}`, {
    type: mime,
  });
}
