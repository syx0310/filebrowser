<template>
  <div class="directory-size-map">
    <div class="map-toolbar card">
      <PathPickerButton v-model:path="chosenPath" v-model:source="chosenSource" :show-files="false" :show-folders="true" :placeholder="$t('sidebar.chooseSource')" />
      <button type="button" class="button button--flat" :disabled="currentPath === '/' || loading" @click="goUp">{{ symbols.up }} {{ $t('sizeMap.up') }}</button>
      <button type="button" class="button" :disabled="!chosenSource || loading" @click="loadTree">{{ $t('sizeMap.refresh') }}</button>
    </div>
    <nav class="map-breadcrumbs" :aria-label="$t('general.path')">
      <template v-for="crumb in breadcrumbs" :key="crumb.path">
        <span v-if="crumb.path !== '/'" aria-hidden="true">{{ symbols.separator }}</span>
        <button type="button" :aria-current="crumb.path === currentPath ? 'page' : undefined" @click="navigate(crumb.path)">{{ crumb.name }}</button>
      </template>
    </nav>
    <div v-if="error" class="map-message error" role="alert">{{ error }}</div>
    <div v-if="loading" class="map-message" role="status">{{ $t('sizeMap.loading') }}</div>
    <template v-if="tree && !loading">
      <div class="map-summary">
        <div><span>{{ $t('sizeMap.indexedSize') }}</span><strong>{{ humanSize(tree.root.size) }}</strong></div>
        <div><span>{{ $t('files.count') }}</span><strong>{{ formatCount(tree.root.fileCount) }}</strong></div>
        <div><span>{{ $t('index.lastScanned') }}</span><strong>{{ lastScan }}</strong></div>
        <p>{{ $t('sizeMap.hint') }}</p>
      </div>
      <p v-if="tree.status !== 'ready'" class="map-message">{{ $t('sizeMap.scanning') }}</p>
      <p v-if="hasRemainder" class="map-message">{{ $t('sizeMap.grouped') }}</p>
      <div class="map-workspace">
        <div class="map-canvas card" ref="canvas">
          <svg v-if="rectangles.length" :viewBox="`0 0 ${mapWidth} ${mapHeight}`" role="group" :aria-label="$t('sizeMap.title')" @keydown.backspace.prevent="goUp">
            <g v-for="(rect, index) in rectangles" :key="`${rect.node.path}:${rect.node.remainder}:${rect.nested}`"
              role="button" tabindex="0" :aria-label="`${nodeName(rect.node)} — ${humanSize(rect.node.size)}`"
              :aria-pressed="selected === rect.node" :class="{ 'map-tile': true, selected: selected === rect.node, remainder: rect.node.remainder }"
              @click.stop="select(rect.node)" @dblclick.stop="enter(rect.node)" @keydown.enter.prevent.stop="enter(rect.node)" @keydown.space.prevent.stop="select(rect.node)">
              <title>{{ nodeName(rect.node) }}&#10;{{ rect.node.path }}&#10;{{ humanSize(rect.node.size) }}{{ symbols.middle }}{{ percent(rect.node.size) }}</title>
              <rect :x="rect.x + 1" :y="rect.y + 1" :width="Math.max(0, rect.width - 2)" :height="Math.max(0, rect.height - 2)" :fill="rect.node.remainder ? '#455361' : rect.color" rx="3" />
              <clipPath :id="`size-map-label-${index}`"><rect :x="rect.x + 6" :y="rect.y + 3" :width="Math.max(0, rect.width - 12)" :height="Math.max(0, rect.height - 6)" /></clipPath>
              <text v-if="rect.width > 46 && rect.height > 20" :x="rect.x + 8" :y="rect.y + 17" :clip-path="`url(#size-map-label-${index})`" :class="{ 'folder-label': rect.node.isDir }">{{ rect.node.isDir ? symbols.folder : '' }}{{ nodeName(rect.node) }}</text>
              <text v-if="rect.width > 72 && rect.height > 43 && !(rect.node.isDir && rect.node.expanded)" :x="rect.x + 8" :y="rect.y + 35" :clip-path="`url(#size-map-label-${index})`" class="tile-size">{{ humanSize(rect.node.size) }}</text>
            </g>
          </svg>
          <div v-else class="map-empty">{{ $t('sizeMap.noArea') }}</div>
        </div>
        <aside class="map-details card">
          <span class="detail-eyebrow">{{ selected?.remainder ? $t('sizeMap.other') : selected?.isDir ? $t('general.folder') : selected ? $t('general.file', { suffix: '' }) : $t('sizeMap.selection') }}</span>
          <h3>{{ selected ? nodeName(selected) : $t('sizeMap.selectHint') }}</h3>
          <template v-if="selected">
            <p class="detail-path">{{ selected.path }}</p>
            <div class="detail-size">{{ humanSize(selected.size) }}</div>
            <p>{{ percent(selected.size) }} {{ $t('sizeMap.ofFolder') }}</p>
            <dl>
              <template v-if="selected.isDir"><dt>{{ $t('files.count') }}</dt><dd>{{ formatCount(selected.fileCount) }}</dd></template>
              <template v-if="selected.modified"><dt>{{ $t('files.lastModified') }}</dt><dd>{{ date(selected.modified) }}</dd></template>
              <template v-if="selected.created"><dt>{{ $t('files.creationTime') }}</dt><dd>{{ date(selected.created) }}</dd></template>
            </dl>
            <p v-if="selected.remainder" class="details-note">{{ $t('sizeMap.grouped') }}</p>
            <button v-if="selected.isDir && selected.path !== currentPath" type="button" class="button" @click="enter(selected)">{{ $t('sizeMap.enter') }}</button>
            <router-link class="open-folder" :to="listingUrl(selected)">{{ $t('sizeMap.openListing') }} {{ symbols.external }}</router-link>
          </template>
          <p class="details-note">{{ $t('sizeMap.colorHint') }}</p>
        </aside>
      </div>
      <div class="map-list card">
        <table>
          <thead><tr><th>{{ $t('general.name') }}</th><th>{{ $t('files.count') }}</th><th>{{ $t('general.size') }}</th></tr></thead>
          <tbody>
            <tr v-for="node in tree.root.children || []" :key="node.path" :class="{ active: selected?.path === node.path }" @click="select(node)" @dblclick="enter(node)">
              <td><button type="button" @click.stop="select(node)" @dblclick.stop="enter(node)" @keydown.enter.prevent="enter(node)">{{ node.isDir ? symbols.folder : '' }}{{ node.name }}</button></td>
              <td>{{ node.isDir ? formatCount(node.fileCount) : symbols.dash }}</td><td>{{ humanSize(node.size) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>

<script>
import { toolsApi } from "@/api";
import { state } from "@/store";
import { getHumanReadableFilesize } from "@/utils/filesizes";
import { formatTimestamp, fromNow } from "@/utils/moment";
import { buildSizeMap, treeHasRemainder } from "@/utils/sizeTreemap";
import { globalVars } from "@/utils/constants";
import PathPickerButton from "@/components/files/PathPickerButton.vue";

export default {
  name: "DirectorySizeMap",
  components: { PathPickerButton },
  data() { return { symbols: { up: '↑', separator: '/', middle: ' · ', folder: '▸ ', dash: '—', external: '↗' }, chosenSource: "", chosenPath: "/", tree: null, selected: null, loading: false, error: "", controller: null, requestId: 0, mapWidth: 1100, mapHeight: 660, resizeObserver: null, initializing: true }; },
  computed: {
    currentPath() { return this.tree?.root.path || this.chosenPath || "/"; },
    rectangles() { return this.tree ? buildSizeMap(this.tree.root, this.mapWidth, this.mapHeight) : []; },
    hasRemainder() { return this.tree && treeHasRemainder(this.tree.root); },
    lastScan() { return this.tree?.lastIndexedUnixTime > 0 ? fromNow(this.tree.lastIndexedUnixTime, state.user?.locale, { unit: "seconds" }) : this.$t("index.unknown"); },
    breadcrumbs() {
      const output = [{ name: this.chosenSource, path: "/" }];
      let path = "/";
      for (const name of this.currentPath.split("/").filter(Boolean)) { path += `${name}/`; output.push({ name, path }); }
      return output;
    },
  },
  watch: {
    '$route.query'() { this.syncRoute(); },
    chosenSource() { if (!this.initializing) this.pickPath(); },
    chosenPath() { if (!this.initializing) this.pickPath(); },
  },
  mounted() {
    document.title = `${globalVars.name} - ${this.$t('sizeMap.title')}`;
    this.syncRoute();
  },
  beforeUnmount() { this.requestId++; this.controller?.abort(); this.resizeObserver?.disconnect(); },
  methods: {
    humanSize: getHumanReadableFilesize,
    formatCount(value) { return value == null ? '—' : value.toLocaleString(); },
    date(value) { return formatTimestamp(value, state.user?.locale); },
    nodeName(node) { return node.remainder ? this.$t('sizeMap.other') : node.name; },
    percent(size) { return `${(this.tree?.root.size > 0 ? size / this.tree.root.size * 100 : 0).toFixed(1)}%`; },
    select(node) { this.selected = node; },
    enter(node) { if (node.isDir) this.navigate(node.path); else this.select(node); },
    goUp() {
      if (this.currentPath === '/' || this.loading) return;
      const parts = this.currentPath.split('/').filter(Boolean);
      parts.pop();
      this.navigate(`/${parts.length ? `${parts.join('/')}/` : ''}`);
    },
    navigate(path) { void this.$router.push({ path: this.$route.path, query: { source: this.chosenSource, path } }); },
    async pickPath() {
      await this.$nextTick();
      if (this.chosenSource === this.$route.query.source && this.chosenPath === (this.$route.query.path || '/')) return;
      this.navigate(this.chosenPath || '/');
    },
    async syncRoute() {
      this.initializing = true;
      this.chosenSource = String(this.$route.query.source || state.sources.current || Object.keys(state.sources.info || {})[0] || '');
      this.chosenPath = String(this.$route.query.path || '/');
      await this.$nextTick();
      this.initializing = false;
      void this.loadTree();
    },
    async loadTree() {
      if (!this.chosenSource) return;
      this.controller?.abort();
      this.controller = new AbortController();
      const requestId = ++this.requestId;
      this.loading = true;
      this.error = '';
      try {
        const result = await toolsApi.sizeTree(this.chosenSource, this.chosenPath, this.controller.signal);
        if (requestId !== this.requestId) return;
        this.tree = result;
        this.selected = null;
      } catch (error) {
        if (requestId !== this.requestId) return;
        this.tree = null;
        this.error = error.status === 404 ? this.$t('sizeMap.notIndexed') : error.status === 403 ? this.$t('tools.accessDenied') : this.$t('sizeMap.unavailable');
      } finally {
        if (requestId === this.requestId) {
          this.loading = false;
          await this.$nextTick();
          this.resizeObserver?.disconnect();
          if (this.$refs.canvas && typeof ResizeObserver !== 'undefined') {
            this.resizeObserver = new ResizeObserver(([entry]) => {
              this.mapWidth = Math.max(1, entry.contentRect.width);
              this.mapHeight = Math.max(1, entry.contentRect.height);
            });
            this.resizeObserver.observe(this.$refs.canvas);
          }
        }
      }
    },
    listingUrl(node) {
      let path = node.path;
      if (!node.isDir && !node.remainder) path = path.slice(0, path.lastIndexOf('/') + 1);
      return `/files/${encodeURIComponent(this.chosenSource)}${path.split('/').map(encodeURIComponent).join('/')}`;
    },
  },
};
</script>

<style scoped>
.map-toolbar {
  display: flex;
  align-items: center;
  gap: .7rem;
  padding: .8rem;
}

.map-toolbar :deep(.unified-path-picker) {
  flex: 1;
}

.map-breadcrumbs {
  display: flex;
  flex-wrap: wrap;
  gap: .4rem;
  align-items: center;
  padding: 1rem 0;
}

.map-breadcrumbs button {
  background: none;
  border: 0;
  color: var(--textSecondary);
  cursor: pointer;
  padding: .3rem;
}

.map-breadcrumbs button[aria-current] {
  color: var(--textPrimary);
  font-weight: 700;
}

.map-summary {
  display: flex;
  gap: 2rem;
  align-items: center;
  margin: 0 0 1rem;
  flex-wrap: wrap;
}

.map-summary div {
  display: flex;
  flex-direction: column;
  gap: .2rem;
}

.map-summary span {
  color: var(--textSecondary);
  font-size: .8rem;
}

.map-summary strong {
  font-size: 1.2rem;
}

.map-summary p {
  color: var(--textSecondary);
  font-size: .85rem;
  margin-left: auto;
}

.map-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 235px;
  gap: 1rem;
  align-items: start;
}

.map-canvas {
  height: clamp(360px, 55vh, 720px);
  min-width: 0;
  padding: .4rem;
  overflow: hidden;
  background: #111923;
}

.map-canvas svg {
  display: block;
  width: 100%;
  height: 100%;
}

.map-tile {
  cursor: pointer;
}

.map-tile > rect {
  stroke: #111923;
  stroke-width: 1.5;
  transition: filter .12s;
}

.map-tile:hover > rect, .map-tile:focus > rect {
  filter: brightness(1.3);
  stroke: #c9e7ff;
  stroke-width: 2;
}

.map-tile.selected > rect {
  stroke: #fff;
  stroke-width: 3;
}

.map-tile text {
  fill: #fff;
  font-size: 12px;
  pointer-events: none;
}

.map-tile .folder-label {
  font-weight: 700;
}

.map-tile .tile-size {
  font-size: 11px;
  fill: #dae7f0;
}

.map-tile.remainder > rect {
  stroke-dasharray: 4 3;
}

.map-details {
  padding: 1.1rem;
  overflow-wrap: anywhere;
}

.detail-eyebrow {
  color: var(--textSecondary);
  font-size: .8rem;
}

.map-details h3 {
  margin: .6rem 0;
}

.detail-path {
  font-size: .8rem;
  color: var(--textSecondary);
}

.detail-size {
  font-size: 1.7rem;
  font-weight: 700;
  margin-top: 1.5rem;
}

.map-details dl {
  display: grid;
  gap: .5rem;
  font-size: .85rem;
}

.map-details dt {
  color: var(--textSecondary);
}

.map-details dd {
  margin: 0 0 .7rem;
}

.details-note {
  color: var(--textSecondary);
  font-size: .8rem;
  line-height: 1.5;
}

.open-folder {
  display: block;
  margin: 1rem 0;
}

.map-message {
  background: var(--surfaceSecondary);
  padding: .8rem 1rem;
  border-radius: .6rem;
  color: var(--textSecondary);
  font-size: .85rem;
}

.map-message.error {
  color: var(--red);
}

.map-empty {
  height: 100%;
  display: grid;
  place-items: center;
  color: #c3ced9;
}

.map-list {
  margin-top: 1rem;
  padding: .8rem;
}

.map-list table {
  width: 100%;
  border-collapse: collapse;
  font-size: .9rem;
}

.map-list th, .map-list td {
  padding: .6rem;
  text-align: right;
  border-bottom: 1px solid var(--divider);
}

.map-list th:first-child, .map-list td:first-child {
  text-align: left;
}

.map-list button {
  background: none;
  color: inherit;
  border: 0;
  text-align: left;
  cursor: pointer;
  overflow-wrap: anywhere;
}

.map-list tr.active {
  background: var(--surfaceSecondary);
}

@media (width <= 1200px) {
  .map-workspace {
    grid-template-columns: 1fr;
  }
  .map-toolbar {
    flex-wrap: wrap;
  }
  .map-summary {
    gap: 1rem;
  }
  .map-canvas {
    height: 480px;
  }
}
</style>
