import { Download, FileJson, Upload } from "lucide-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { api } from "../api/client";
import { useSession } from "../session";
import {
  Button,
  Dialog,
  ErrorState,
  Page,
  Panel,
  StatusBadge,
} from "../components/ui";

export function Exchange() {
  const { client } = useSession();
  const s = api(client!);
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => s.channels(signal),
  });
  const [selected, setSelected] = useState<number[]>([]),
    [secretWarning, setSecretWarning] = useState(false),
    [fileName, setFileName] = useState(""),
    [document, setDocument] = useState<unknown>(null);
  const input = useRef<HTMLInputElement>(null);
  const exp = useMutation({
    mutationFn: async ({ secrets }: { secrets: boolean }) => {
      const data = await s.exportData(secrets, selected);
      const blob = new Blob([JSON.stringify(data, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = window.document.createElement("a");
      a.href = url;
      a.download = `meta-gateway-${secrets ? "secret-" : "metadata-"}export.json`;
      a.click();
      URL.revokeObjectURL(url);
    },
    onSuccess: () => setSecretWarning(false),
  });
  const imp = useMutation({
    mutationFn: () => s.importData(document),
    onSuccess: () => {
      setDocument(null);
      setFileName("");
      if (input.current) input.current.value = "";
    },
  });
  async function choose(file?: File) {
    if (!file) return;
    setFileName(file.name);
    try {
      setDocument(JSON.parse(await file.text()));
    } catch {
      setDocument(null);
    }
  }
  return (
    <Page
      title="Exchange"
      description="Move channel assets through the versioned Meta Gateway exchange format."
    >
      <div className="exchange-grid">
        <Panel title="Export channels">
          <p>
            Metadata exports are safe to inspect but cannot be imported. Secret
            exports are importable and must be handled as credentials.
          </p>
          <div className="selection-list">
            <label className="check">
              <input
                type="checkbox"
                checked={selected.length === 0}
                onChange={() => setSelected([])}
              />
              <span>All channels</span>
            </label>
            {channels.data?.map((c) => (
              <label className="check" key={c.id}>
                <input
                  type="checkbox"
                  checked={selected.includes(c.id)}
                  onChange={(e) =>
                    setSelected(
                      e.target.checked
                        ? [...selected, c.id]
                        : selected.filter((id) => id !== c.id),
                    )
                  }
                />
                <span>{c.name}</span>
              </label>
            ))}
          </div>
          {exp.error && <ErrorState error={exp.error} />}
          <div className="toolbar">
            <Button
              variant="secondary"
              icon={<Download size={16} />}
              disabled={exp.isPending}
              onClick={() => exp.mutate({ secrets: false })}
            >
              Download metadata
            </Button>
            <Button
              variant="danger"
              icon={<Download size={16} />}
              disabled={exp.isPending}
              onClick={() => setSecretWarning(true)}
            >
              Export with secrets
            </Button>
          </div>
        </Panel>
        <Panel title="Import assets">
          <div className="drop-zone" onClick={() => input.current?.click()}>
            <FileJson size={28} />
            <strong>{fileName || "Choose a JSON exchange file"}</strong>
            <span>Maximum 10 MiB</span>
            <input
              ref={input}
              hidden
              type="file"
              accept="application/json,.json"
              onChange={(e) => choose(e.target.files?.[0])}
            />
          </div>
          {document !== null && (
            <div className="result-strip">
              <StatusBadge value="ready" />
              <span>JSON parsed and ready to import.</span>
            </div>
          )}
          {imp.error && <ErrorState error={imp.error} />}
          <Button
            icon={<Upload size={16} />}
            disabled={!document || imp.isPending}
            onClick={() => imp.mutate()}
          >
            {imp.isPending ? "Importing..." : "Import assets"}
          </Button>
          {imp.data && (
            <div className="import-result">
              <h3>Import complete</h3>
              <div>
                <span>
                  Created <strong>{imp.data.created_count}</strong>
                </span>
                <span>
                  Updated <strong>{imp.data.updated_count}</strong>
                </span>
                <span>
                  Adopted <strong>{imp.data.adopted_count}</strong>
                </span>
                <span>
                  Discovery failures{" "}
                  <strong>{imp.data.discovery_failure_count}</strong>
                </span>
              </div>
            </div>
          )}
        </Panel>
      </div>
      {secretWarning && (
        <Dialog
          danger
          title="Export channel secrets?"
          onClose={() => setSecretWarning(false)}
          actions={
            <>
              <Button
                variant="secondary"
                onClick={() => setSecretWarning(false)}
              >
                Cancel
              </Button>
              <Button
                variant="danger"
                disabled={exp.isPending}
                onClick={() => exp.mutate({ secrets: true })}
              >
                Download sensitive export
              </Button>
            </>
          }
        >
          <p>
            This file contains plaintext upstream API keys. Store it securely,
            do not commit it, and delete it when no longer needed. Its contents
            will not be previewed here.
          </p>
        </Dialog>
      )}
    </Page>
  );
}
