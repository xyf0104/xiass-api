import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const groupsViewSource = readFileSync(
  resolve(currentDir, "../GroupsView.vue"),
  "utf8",
);

describe("groups model allowlist layout", () => {
  it("keeps the toolbar outside of the scrolling list content", () => {
    expect(groupsViewSource).toContain("overflow-hidden rounded-lg border");
    expect(
      groupsViewSource.match(/class="max-h-64 space-y-2 overflow-y-auto p-2"/g),
    ).toHaveLength(2);

    // A separate user/account allowlist table intentionally has a sticky
    // header. Guard the two models-list scrollers themselves instead of
    // rejecting unrelated sticky UI anywhere in this large view.
    const modelListScrollers = [
      groupsViewSource.indexOf('v-if="createModelAllowlistLoading"'),
      groupsViewSource.indexOf('v-if="editModelAllowlistLoading"'),
    ];
    for (const scroller of modelListScrollers) {
      expect(scroller).toBeGreaterThan(0);
      expect(groupsViewSource.slice(scroller - 160, scroller)).not.toContain("sticky top-0");
    }
  });

  it("uses a wide dialog and keeps model pricing controls responsive", () => {
    expect(groupsViewSource).toContain('width="wide"');
    expect(groupsViewSource).toContain(
      "btn btn-secondary shrink-0 whitespace-nowrap",
    );
  });
});
