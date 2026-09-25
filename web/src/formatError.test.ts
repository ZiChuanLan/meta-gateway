import { describe, expect, it } from "vitest";
import { categorizeError } from "./errorCatalog";
import { formatErrorMessage } from "./formatError";
import { translate } from "./i18n";

const t = (key: string, vars?: Record<string, string | number>) =>
  translate("zh-CN", key, vars);

/**
 * `invalid_payload` is the adapter's verdict on an upstream 2xx body it cannot
 * read. It sat in the "config" class, so a correctly configured channel whose
 * upstream simply does not speak OpenAI was told to "check Base URL, connection
 * type and credentials" — which is where the investigation then went, wrongly,
 * while the real cause stayed in a JSON key name. Measured case: TypeSafe's
 * `GET /v1/models` answers 200 with {"models":[{"name":"jev-latest"}]}.
 */
describe("upstream response shape errors", () => {
  it("classifies an unreadable upstream body on its own, not as a config error", () => {
    expect(categorizeError("invalid_payload").class).toBe("upstream_shape");
  });

  it("stops sending the operator back to a Base URL that is already right", () => {
    const message = formatErrorMessage("invalid_payload", t);
    expect(message).toContain("上游响应无法识别");
    expect(message).not.toContain("Base URL");
    expect(message).not.toContain("凭据");
    // The actionable next step is the mapping, not the credential.
    expect(message).toContain("字段映射");
  });

  it("keeps genuine configuration failures pointing at the connection", () => {
    expect(categorizeError("invalid_base_url").class).toBe("config");
    expect(formatErrorMessage("invalid_base_url", t)).toContain("Base URL");
  });
});

/**
 * A plugin market install fails outside the gateway: a registry the gateway
 * cannot reach, a release that cannot be resolved, an artifact host that
 * refused. Until 2026-09-25 every one of those answered a bare
 * `internal_error` (class "server", "check the gateway logs" — logs that had
 * nothing in them), so the console blamed the gateway for a network the
 * operator could actually fix.
 */
describe("plugin market errors", () => {
  it("tells the operator the market itself is unreachable", () => {
    expect(categorizeError("plugin_market_unavailable").class).toBe("network");
    const message = formatErrorMessage("plugin_market_unavailable", t);
    expect(message).toContain("插件市场不可用");
    // The one fact that unblocks a CN host reaching GitHub only through a proxy.
    expect(message).toContain("代理");
  });

  it("keeps the package-download failure out of the channel vocabulary", () => {
    const message = formatErrorMessage("plugin_release_fetch", t);
    expect(message).toContain("取不到安装包");
    expect(message).not.toContain("Base URL");
    expect(message).not.toContain("凭据");
  });

  it("keeps an HTTP status carried in the code instead of flattening it", () => {
    // The status survives so the console can say "upstream refused (403)"
    // rather than an untitled plugin failure. 403 itself belongs to the shared
    // auth class (the taxonomy reads an upstream 403 as a permission problem);
    // 5xx is the shape that means "the artifact host broke".
    const refused = categorizeError("plugin_artifact_download_status_403");
    expect(refused.status).toBe(403);
    const broken = categorizeError("plugin_artifact_download_status_502");
    expect(broken.class).toBe("upstream_reject");
  });

  it("blames the package, not the gateway, when a manifest is wrong", () => {
    expect(categorizeError("plugin_manifest_entrypoint_missing").class).toBe("config");
    expect(categorizeError("plugin_stage_create").class).toBe("server");
  });
});
