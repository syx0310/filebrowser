import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/notify", () => ({ notify: { showError: vi.fn() } }));
vi.mock("@/utils/url.js", () => ({
  getApiPath: (_path, params) => JSON.stringify(params),
}));
vi.mock("./utils", () => ({ fetchURL: vi.fn(), fetchJSON: vi.fn() }));

import { fetchURL, fetchJSON } from "./utils.ts";
import { search, sizeTree } from "./tools.js";

describe("search result limit", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchURL.mockResolvedValue({ json: async () => [] });
  });

  it("passes the selected limit with repeated scopes and terms", async () => {
    await search(null, ["one", "two"], "", false, {
      limit: 750,
      terms: ["document"],
      perSourceScopes: [{ source: "one", path: "/docs" }, { source: "two", path: "/" }],
    });
    expect(JSON.parse(fetchURL.mock.calls[0][0])).toMatchObject({
      limit: "750", terms: ["document"], scope: ["one:/docs", "two:/"],
    });
  });

  it("leaves quick search at the server default", async () => {
    await search("/", "one", "document");
    expect(JSON.parse(fetchURL.mock.calls[0][0])).not.toHaveProperty("limit");
  });

  it("preserves the size viewer request", async () => {
    await search("/", "one", "", true);
    expect(JSON.parse(fetchURL.mock.calls[0][0])).toMatchObject({ largest: "true" });
    expect(JSON.parse(fetchURL.mock.calls[0][0])).not.toHaveProperty("limit");
  });

  it("requests two levels of indexed directory sizes with cancellation", async () => {
    const controller = new AbortController();
    await sizeTree('one', '/photos/', controller.signal);
    expect(JSON.parse(fetchJSON.mock.calls[0][0])).toEqual({ source: 'one', path: '/photos/', depth: '2', limit: '80' });
    expect(fetchJSON.mock.calls[0][1].signal).toBe(controller.signal);
  });

  it("passes the selected directory-map item limit to the index query", async () => {
    await sizeTree('one', '/photos/', undefined, 500);
    expect(JSON.parse(fetchJSON.mock.calls[0][0])).toMatchObject({ limit: '500' });
  });
});
