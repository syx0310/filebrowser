import { createApp, nextTick, reactive } from "vue";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { sizeTree } = vi.hoisted(() => ({ sizeTree: vi.fn() }));
vi.mock("@/api", () => ({ toolsApi: { sizeTree } }));
vi.mock("@/components/files/PathPickerButton.vue", () => ({ default: { render: () => null } }));
import DirectorySizeMap from "./DirectorySizeMap.vue";

const tree = {
  source: "demo", status: "ready", lastIndexedUnixTime: 1792890000,
  root: { name: "demo", path: "/", size: 100, isDir: true, expanded: true, fileCount: 2, children: [
    { name: "folder", path: "/folder/", size: 100, isDir: true, expanded: true, fileCount: 2, children: [
      { name: "one.bin", path: "/folder/one.bin", size: 70, isDir: false },
      { name: "two.bin", path: "/folder/two.bin", size: 30, isDir: false },
    ] },
  ] },
};
let app, root, route, push;
async function mount() {
  route = reactive({ path: "/tools/sizeViewer", query: { source: "demo" } });
  push = vi.fn(async (value) => { route.query = value.query; });
  root = document.createElement("div");
  document.body.append(root);
  app = createApp(DirectorySizeMap);
  app.config.globalProperties.$t = (key) => key;
  app.config.globalProperties.$route = route;
  app.config.globalProperties.$router = { push };
  app.component("router-link", { props: ['to'], template: '<a :href="to"><slot /></a>' });
  app.mount(root);
  await vi.waitFor(() => expect(root.querySelector('svg')).not.toBeNull());
}

describe("directory map navigation", () => {
  beforeEach(() => { sizeTree.mockReset(); sizeTree.mockResolvedValue(tree); });
  afterEach(() => { app?.unmount(); root?.remove(); });

  it("loads from the index, selects file details and drills into directories", async () => {
    await mount();
    expect(sizeTree).toHaveBeenCalledWith("demo", "/", expect.any(AbortSignal));
    const one = root.querySelector('g[aria-label^="one.bin"]');
    one.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await nextTick();
    expect(root.querySelector('.map-details').textContent).toContain('/folder/one.bin');
    expect(root.querySelector('.open-folder').getAttribute('href')).toBe('/files/demo/folder/');
    sizeTree.mockResolvedValueOnce({ ...tree, root: tree.root.children[0] });
    root.querySelector('g[aria-label^="folder —"]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await vi.waitFor(() => expect(sizeTree).toHaveBeenLastCalledWith("demo", "/folder/", expect.any(AbortSignal)));
    await vi.waitFor(() => expect(root.querySelector('.map-breadcrumbs [aria-current]').textContent).toBe('folder'));
    root.querySelector('.map-toolbar button').click();
    await vi.waitFor(() => expect(push).toHaveBeenLastCalledWith({ path: '/tools/sizeViewer', query: { source: 'demo', path: '/' } }));
  });

  it("cancels stale requests and does not replace the new folder with an old result", async () => {
    await mount();
    let finishOld;
    sizeTree.mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }));
    route.query = { source: 'demo', path: '/old/' };
    await vi.waitFor(() => expect(sizeTree).toHaveBeenCalledTimes(2));
    const signal = sizeTree.mock.calls[1][2];
    sizeTree.mockResolvedValueOnce({ ...tree, root: { ...tree.root, path: '/new/', name: 'new' } });
    route.query = { source: 'demo', path: '/new/' };
    await vi.waitFor(() => expect(root.querySelector('.map-breadcrumbs [aria-current]').textContent).toBe('new'));
    expect(signal.aborted).toBe(true);
    finishOld({ ...tree, root: { ...tree.root, path: '/old/' } });
    await nextTick();
    expect(root.querySelector('.map-breadcrumbs [aria-current]').textContent).toBe('new');
  });
});
