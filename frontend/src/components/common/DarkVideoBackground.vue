<template>
  <div class="theme-video-background pointer-events-none fixed inset-0" aria-hidden="true">
    <video
      ref="lightVideoRef"
      class="theme-video-background__media theme-video-background__media--light"
      :class="[
        { 'theme-video-background__media--active': !isDark },
        { 'theme-video-background__media--blurred': blurred }
      ]"
      :src="lightSrc"
      :poster="lightPoster"
      muted
      loop
      playsinline
      preload="metadata"
    ></video>
    <video
      ref="darkVideoRef"
      class="theme-video-background__media theme-video-background__media--dark"
      :class="[
        { 'theme-video-background__media--active': isDark },
        { 'theme-video-background__media--blurred': blurred }
      ]"
      :src="darkSrc"
      :poster="darkPoster"
      muted
      loop
      playsinline
      preload="metadata"
    ></video>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'

const props = withDefaults(defineProps<{
  blurred?: boolean
  darkSrc?: string
  lightSrc?: string
  darkPoster?: string
  lightPoster?: string
  darkPlaybackRate?: number
  lightPlaybackRate?: number
}>(), {
  blurred: false,
  darkSrc: '/media/xiass-dark-bokeh.mp4',
  lightSrc: '/media/xiass-light-water.mp4',
  darkPoster: '/media/xiass-dark-bokeh-poster.png',
  lightPoster: '/media/xiass-light-water-poster.png',
  darkPlaybackRate: 0.72,
  lightPlaybackRate: 0.42
})

const darkVideoRef = ref<HTMLVideoElement | null>(null)
const lightVideoRef = ref<HTMLVideoElement | null>(null)
const isDark = ref(document.documentElement.classList.contains('dark'))
let themeObserver: MutationObserver | null = null

function configurePlaybackRate(video: HTMLVideoElement | null, playbackRate: number) {
  if (!video) return
  video.defaultPlaybackRate = playbackRate
  video.playbackRate = playbackRate
}

function syncPlaybackWithTheme() {
  isDark.value = document.documentElement.classList.contains('dark')

  void nextTick(() => {
    const activeVideo = isDark.value ? darkVideoRef.value : lightVideoRef.value
    const inactiveVideo = isDark.value ? lightVideoRef.value : darkVideoRef.value
    configurePlaybackRate(darkVideoRef.value, props.darkPlaybackRate)
    configurePlaybackRate(lightVideoRef.value, props.lightPlaybackRate)
    inactiveVideo?.pause()
    void activeVideo?.play().catch(() => undefined)
  })
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
.theme-video-background {
  background: #cdd8df;
  transition: background-color 520ms ease;
}

:global(.dark) .theme-video-background {
  background: #061720;
}

.theme-video-background__media {
  position: absolute;
  inset: 0;
  height: 100%;
  width: 100%;
  object-fit: cover;
  opacity: 0;
  transform: scale(1.03);
  transform-origin: center;
  transition: opacity 520ms ease, filter 520ms ease;
}

.theme-video-background__media--active {
  opacity: 1;
}

.theme-video-background__media--light {
  filter: grayscale(1) brightness(0.7) contrast(3.4) blur(2px);
  mix-blend-mode: screen;
}

.theme-video-background__media--light.theme-video-background__media--active {
  opacity: 0.72;
}

.theme-video-background__media--blurred {
  filter: blur(10px) brightness(0.72) saturate(0.92);
  transform: scale(1.06);
}

.theme-video-background__media--light.theme-video-background__media--blurred {
  filter: grayscale(1) brightness(0.68) contrast(3.2) blur(10px);
  mix-blend-mode: screen;
}

.theme-video-background__media--light.theme-video-background__media--blurred.theme-video-background__media--active {
  opacity: 0.65;
}
</style>
