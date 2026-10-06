// @vitest-environment node
import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it("keeps beta publication isolated from latest and major/minor stable tags", () => {
  const workflow = readFileSync("../.github/workflows/release.yml", "utf8");
  expect(workflow).toContain("flavor: latest=false");
  expect(workflow).toContain(
    "type=raw,value=latest,enable=${{ steps.channel.outputs.prerelease == 'false' }}",
  );
  expect(workflow).toContain(
    "type=semver,pattern={{major}}.{{minor}},enable=${{ steps.channel.outputs.prerelease == 'false' }}",
  );
  expect(workflow).toContain("type=raw,value=beta");
  expect(
    workflow.match(/prerelease: \$\{\{ steps.channel.outputs.prerelease == 'true' \}\}/g),
  ).toHaveLength(2);
  expect(workflow).toContain("needs: wait-for-ci");
});
