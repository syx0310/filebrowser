import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildIndexInfoRows } from "./IndexInfo.vue";

describe("index last scan display", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-24T04:05:00Z"));
  });

  afterEach(() => vi.useRealTimers());

  function lastScan(lastIndex, locale = "en") {
    return buildIndexInfoRows({ lastIndex }, (key) => key, locale)
      .find((row) => row.id === "lastScanned").value;
  }

  it("shows a recent API Unix timestamp as minutes ago, not decades ago", () => {
    const apiTimestamp = Date.parse("2026-10-24T04:00:00Z") / 1000;
    expect(lastScan(apiTimestamp)).toBe("5 minutes ago");
    expect(lastScan(apiTimestamp, "zhCN")).toBe("5分钟前");
  });

  it.each([undefined, null, 0, "", -62135596800, NaN, Infinity, "invalid"])(
    "shows unknown when no valid scan time is available: %s",
    (lastIndex) => {
      expect(lastScan(lastIndex)).toBe("index.unknown");
    },
  );
});
