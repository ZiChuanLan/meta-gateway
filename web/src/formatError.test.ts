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
