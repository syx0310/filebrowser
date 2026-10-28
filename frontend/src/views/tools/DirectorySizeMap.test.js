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
async function mount(query = { source: "demo" }) {
  route = reactive({ path: "/tools/sizeViewer", query });
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
  beforeEach(() => { localStorage.clear(); sizeTree.mockReset(); sizeTree.mockResolvedValue(tree); });
  afterEach(() => { app?.unmount(); root?.remove(); vi.restoreAllMocks(); });

  it("loads from the index, selects file details and drills into directories", async () => {
    await mount();
    expect(sizeTree).toHaveBeenCalledWith("demo", "/", expect.any(AbortSignal), 80);
    const one = root.querySelector('g[aria-label^="one.bin"]');
    one.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await nextTick();
    expect(root.querySelector('.map-details').textContent).toContain('/folder/one.bin');
    expect(root.querySelector('.open-folder').getAttribute('href')).toBe('/files/demo/folder/');
    sizeTree.mockResolvedValueOnce({ ...tree, root: tree.root.children[0] });
    root.querySelector('g[aria-label^="folder —"]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await vi.waitFor(() => expect(sizeTree).toHaveBeenLastCalledWith("demo", "/folder/", expect.any(AbortSignal), 80));
    await vi.waitFor(() => expect(root.querySelector('.map-breadcrumbs [aria-current]').textContent).toBe('folder'));
    root.querySelector('.map-toolbar button').click();
    await vi.waitFor(() => expect(push).toHaveBeenLastCalledWith({ path: '/tools/sizeViewer', query: { source: 'demo', path: '/', limit: '80' } }));
  });

  it("applies the chosen limit to new queries and retains it while navigating and reopening", async () => {
    await mount();
    const input = root.querySelector('#size-map-item-limit');
    input.value = '200';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    root.querySelector('.map-display-options').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(sizeTree).toHaveBeenLastCalledWith('demo', '/', expect.any(AbortSignal), 200));
    expect(localStorage.getItem('directoryMapItemLimit')).toBe('200');
    expect(route.query.limit).toBe('200');
    await vi.waitFor(() => expect(root.querySelector('g[aria-label^="folder —"]')).not.toBeNull());
    sizeTree.mockResolvedValueOnce({ ...tree, root: tree.root.children[0] });
    root.querySelector('g[aria-label^="folder —"]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await vi.waitFor(() => expect(sizeTree).toHaveBeenLastCalledWith('demo', '/folder/', expect.any(AbortSignal), 200));
    app.unmount();
    root.remove();
    await mount();
    expect(sizeTree).toHaveBeenLastCalledWith('demo', '/', expect.any(AbortSignal), 200);
    expect(root.querySelector('#size-map-item-limit').value).toBe('200');
  });

  it("uses the limit in a shared URL before the browser preference", async () => {
    localStorage.setItem('directoryMapItemLimit', '25');
    await mount({ source: 'demo', limit: '1000' });
    expect(sizeTree).toHaveBeenCalledWith('demo', '/', expect.any(AbortSignal), 1000);
    route.query = { source: 'demo', limit: '25' };
    await vi.waitFor(() => expect(sizeTree).toHaveBeenLastCalledWith('demo', '/', expect.any(AbortSignal), 25));
    expect(root.querySelector('#size-map-item-limit').value).toBe('25');
  });

  it("rejects invalid input without querying and falls back from a corrupt saved preference", async () => {
    localStorage.setItem('directoryMapItemLimit', 'invalid');
    await mount();
    const input = root.querySelector('#size-map-item-limit');
    expect(input.value).toBe('80');
    input.value = '1001';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    root.querySelector('.map-display-options').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await nextTick();
    expect(root.querySelector('.limit-error').textContent).toBe('sizeMap.invalidLimit');
    expect(sizeTree).toHaveBeenCalledTimes(1);
    expect(push).not.toHaveBeenCalled();
  });

  it("loads normally when browser storage is unavailable", async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('unavailable'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('unavailable'); });
    await mount();
    expect(sizeTree).toHaveBeenCalledWith('demo', '/', expect.any(AbortSignal), 80);
  });

  it("lists files and directories together in descending size order", async () => {
    sizeTree.mockResolvedValue({ ...tree, root: { ...tree.root, children: [
      { name: 'small', path: '/small', size: 5, isDir: false },
      { name: 'folder', path: '/folder/', size: 30, isDir: true },
      { name: 'large', path: '/large', size: 65, isDir: false },
    ] } });
    await mount();
    expect([...root.querySelectorAll('.map-list tbody button')].map(node => node.textContent)).toEqual(['large', '▸ folder', 'small']);
    expect(root.querySelector('.map-list th[aria-sort="descending"]')).not.toBeNull();
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
