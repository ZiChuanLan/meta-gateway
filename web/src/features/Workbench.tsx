import { useState } from "react";
import { Image as ImageIcon, MessageSquare } from "lucide-react";
import { Page, Tabs } from "../components/ui";
import { useI18n } from "../i18n";
import ImageStudio from "./workbench/ImageStudio";
import Playground from "./workbench/Playground";

/**
 * The workbench is the run-it surface: generate an image, hold a conversation.
 * The model capability registry used to be a third tab here; it is catalog data
 * rather than a thing you run, so it now lives behind the Models page's tools
 * menu (`CapabilityRegistryDialog`) next to the model list it describes.
 */
type TabValue = "images" | "text";

export default function Workbench() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabValue>("images");
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
      <div hidden={tab !== "images"}><ImageStudio active={tab === "images"} /></div>
      <div hidden={tab !== "text"}><Playground active={tab === "text"} /></div>
    </Page>
  );
}
