import { useMemo, useState } from "react";
import { Image as ImageIcon, MessageSquare } from "lucide-react";
import { api } from "../api/client";
import { Page, Tabs } from "../components/ui";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { adminRunner, memberRunner, type WorkbenchRunner } from "./workbench/runner";
import ImageStudio from "./workbench/ImageStudio";
import Playground from "./workbench/Playground";

/**
 * The workbench: generate an image, hold a conversation.
 *
 * The two roles run different machines behind the same page (see
 * workbench/runner.ts): staff probe through /admin/try/* with the deployment
 * bearer, a member probes with one of their own tokens through the real /v1, so
 * their quota, billing and request log behave exactly as they will for their own
 * code. What they see is identical, which is the point — an earlier split had a
 * second, simpler workbench for members, and the same page then looked like two
 * different products.
 *
 * The model capability registry used to be a third tab here; it is catalog data
 * rather than a thing you run, so it now lives behind the Models page's tools
 * menu (`CapabilityRegistryDialog`) next to the model list it describes.
 */
type TabValue = "images" | "text";

export default function Workbench() {
  const { client, role } = useSession();
  const { t } = useI18n();
  const [tab, setTab] = useState<TabValue>("images");
  // Built once per session, not per render: the member runner caches the key it
  // remembered from the catalogue and the plaintext tokens it revealed, and a
  // fresh instance every render threw both away — the probe then reported "no
  // usable token" the moment anything re-rendered between catalogue and send.
  const runner: WorkbenchRunner = useMemo(
    () => (client && role !== "member" ? adminRunner(api(client), t) : memberRunner()),
    [client, role, t],
  );
  return (
    <Page title={t("workbench.title")} description={t("workbench.desc")}>
      <Tabs
        items={[
          { value: "images", label: t("workbench.tabImages"), icon: <ImageIcon size={13} /> },
          { value: "text", label: t("workbench.tabText"), icon: <MessageSquare size={13} /> },
        ]}
        active={tab}
        onChange={(value) => setTab(value as TabValue)}
      />
      <div hidden={tab !== "images"}>
        <ImageStudio active={tab === "images"} runner={runner} />
      </div>
      <div hidden={tab !== "text"}>
        <Playground active={tab === "text"} runner={runner} />
      </div>
    </Page>
  );
}
