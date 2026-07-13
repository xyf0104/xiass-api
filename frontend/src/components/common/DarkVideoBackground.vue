<template>
  <video
    ref="videoRef"
    class="dark-video-background pointer-events-none fixed inset-0 h-full w-full object-cover"
    :class="[
      always ? 'block' : 'hidden dark:block',
      { 'dark-video-background--blurred': blurred }
    ]"
    autoplay
    muted
    loop
    playsinline
    preload="auto"
    aria-hidden="true"
  >
    <source src="/media/nowind-dark-bokeh.mp4" type="video/mp4" />
  </video>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

const { always = false, blurred = false } = defineProps<{
  always?: boolean
  blurred?: boolean
}>()

const videoRef = ref<HTMLVideoElement | null>(null)
let themeObserver: MutationObserver | null = null

function syncPlaybackWithTheme() {
  const video = videoRef.value
  if (!video) return

  if (always || document.documentElement.classList.contains('dark')) {
    void video.play().catch(() => undefined)
  } else {
    video.pause()
  }
}

onMounted(() => {
  syncPlaybackWithTheme()
  themeObserver = new MutationObserver(syncPlaybackWithTheme)
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class']
  })
})

onUnmounted(() => {
  themeObserver?.disconnect()
})
</script>

<style scoped>
.dark-video-background {
  transform: scale(1.01);
  transform-origin: center;
}

.dark-video-background--blurred {
  filter: blur(10px) brightness(0.72) saturate(0.92);
  transform: scale(1.06);
}
</style>
