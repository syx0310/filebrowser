<template>
  <section class="size-tool">
    <nav class="size-modes" :aria-label="$t('sizeMap.view')">
      <button type="button" :class="{ active: !largest }" :aria-pressed="!largest" @click="setMode(false)">{{ $t('sizeMap.title') }}</button>
      <button type="button" :class="{ active: largest }" :aria-pressed="largest" @click="setMode(true)">{{ $t('sizeMap.largestFiles') }}</button>
    </nav>
    <LargestFiles v-if="largest" />
    <DirectorySizeMap v-else />
  </section>
</template>

<script>
import DirectorySizeMap from "./DirectorySizeMap.vue";
import LargestFiles from "./LargestFiles.vue";

export default {
  name: "SizeViewer",
  components: { DirectorySizeMap, LargestFiles },
  computed: {
    largest() { return this.$route.query.mode === "largest"; },
  },
  methods: {
    setMode(largest) {
      const query = { ...this.$route.query };
      if (largest) query.mode = "largest";
      else delete query.mode;
      void this.$router.push({ path: this.$route.path, query });
    },
  },
};
</script>

<style scoped>
.size-tool {
  width: 100%;
  padding: 0 1rem 2rem;
  box-sizing: border-box;
}

.size-modes {
  display: flex;
  gap: .4rem;
  margin: .4rem 0 1rem;
}

.size-modes button {
  border: 1px solid var(--divider);
  background: var(--surfaceSecondary);
  color: var(--textPrimary);
  border-radius: .6rem;
  padding: .6rem 1rem;
  cursor: pointer;
}

.size-modes button.active {
  background: var(--primaryColor);
  color: white;
}
</style>
