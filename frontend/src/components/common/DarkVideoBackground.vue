<template>
  <div class="theme-video-background pointer-events-none fixed inset-0" aria-hidden="true">
    <img
      ref="lightPosterRef"
      class="theme-video-background__media theme-video-background__poster theme-video-background__media--light"
      :class="[
        { 'theme-video-background__media--active': !isDark },
        { 'theme-video-background__media--blurred': blurred }
      ]"
      :src="lightPoster"
      alt=""
      @load="handlePosterSettled('light')"
      @error="handlePosterSettled('light')"
    />
    <video
      ref="lightVideoRef"
      class="theme-video-background__media theme-video-background__video theme-video-background__media--light"
      :class="[
        { 'theme-video-background__media--active': !isDark && !lightLoopFading },
        { 'theme-video-background__media--blurred': blurred }
      ]"
      :src="lightSrc"
      :poster="lightPoster"
      muted
      playsinline
      preload="auto"
      @timeupdate="handleTimeUpdate('light')"
      @ended="handleEnded('light')"
    ></video>
    <img
      ref="darkPosterRef"
      class="theme-video-background__media theme-video-background__poster theme-video-background__media--dark"
      :class="[
        { 'theme-video-background__media--active': isDark },
        { 'theme-video-background__media--blurred': blurred }
      ]"
      :src="darkPoster"
      alt=""
      @load="handlePosterSettled('dark')"
      @error="handlePosterSettled('dark')"
    />
    <video
      ref="darkVideoRef"
      class="theme-video-background__media theme-video-background__video theme-video-background__media--dark"
      :class="[
        { 'theme-video-background__media--active': isDark && !darkLoopFading },
        { 'theme-video-background__media--blurred': blurred }
      ]"
      :src="darkSrc"
      :poster="darkPoster"
      muted
      playsinline
      preload="auto"
      @timeupdate="handleTimeUpdate('dark')"
      @ended="handleEnded('dark')"
    ></video>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { notifyThemeBackgroundReady } from '@/utils/theme'

type ThemeVideo = 'light' | 'dark'

const props = withDefaults(defineProps<{
  blurred?: boolean
  darkSrc?: string
  lightSrc?: string
  darkPoster?: string
  lightPoster?: string
  darkPlaybackRate?: number
  lightPlaybackRate?: number
  loopCrossfadeSeconds?: number
}>(), {
  blurred: false,
  darkSrc: '/media/xiass-dark-bokeh.mp4',
  lightSrc: '/media/xiass-light-water.mp4',
  darkPoster: '/media/xiass-dark-bokeh-poster.png',
  lightPoster: '/media/xiass-light-water-poster.png',
  darkPlaybackRate: 0.72,
  lightPlaybackRate: 0.42,
  loopCrossfadeSeconds: 1.15
})

const darkVideoRef = ref<HTMLVideoElement | null>(null)
const lightVideoRef = ref<HTMLVideoElement | null>(null)
const darkPosterRef = ref<HTMLImageElement | null>(null)
const lightPosterRef = ref<HTMLImageElement | null>(null)
const darkLoopFading = ref(false)
const lightLoopFading = ref(false)
const isDark = ref(document.documentElement.classList.contains('dark'))
const resetInProgress: Record<ThemeVideo, boolean> = { light: false, dark: false }
const posterSettled: Record<ThemeVideo, boolean> = { light: false, dark: false }
let themeObserver: MutationObserver | null = null
let unmounted = false

function videoFor(theme: ThemeVideo): HTMLVideoElement | null {
  return theme === 'dark' ? darkVideoRef.value : lightVideoRef.value
}

function posterFor(theme: ThemeVideo): HTMLImageElement | null {
  return theme === 'dark' ? darkPosterRef.value : lightPosterRef.value
}

function playbackRateFor(theme: ThemeVideo): number {
  return theme === 'dark' ? props.darkPlaybackRate : props.lightPlaybackRate
}

function themeIsVisible(theme: ThemeVideo): boolean {
  return theme === 'dark' ? isDark.value : !isDark.value
}

function handlePosterSettled(theme: ThemeVideo) {
  posterSettled[theme] = true
  if (themeIsVisible(theme)) notifyThemeBackgroundReady(theme)
}

function notifyIfActivePosterSettled() {
  const theme: ThemeVideo = isDark.value ? 'dark' : 'light'
  const poster = posterFor(theme)
  if (poster?.complete) posterSettled[theme] = true
  if (posterSettled[theme]) notifyThemeBackgroundReady(theme)
}

function setLoopFading(theme: ThemeVideo, fading: boolean) {
  if (theme === 'dark') {
    darkLoopFading.value = fading
  } else {
    lightLoopFading.value = fading
  }
}

function configurePlaybackRate(video: HTMLVideoElement | null, playbackRate: number) {
  if (!video) return
  video.defaultPlaybackRate = playbackRate
  video.playbackRate = playbackRate
}

function wait(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds))
}

function waitForFirstFrame(video: HTMLVideoElement): Promise<void> {
  return new Promise((resolve) => {
    let settled = false
    const finish = () => {
      if (settled) return
      settled = true
      resolve()
    }
    window.setTimeout(finish, 300)
    if (typeof video.requestVideoFrameCallback === 'function') {
      video.requestVideoFrameCallback(() => finish())
      return
    }
    window.requestAnimationFrame(() => window.requestAnimationFrame(finish))
  })
}

async function resetLoop(theme: ThemeVideo) {
  if (resetInProgress[theme]) return
  const video = videoFor(theme)
  if (!video) return

  resetInProgress[theme] = true
  setLoopFading(theme, true)
  await wait(950)
  if (unmounted) return

  video.pause()
  video.currentTime = 0
  configurePlaybackRate(video, playbackRateFor(theme))

  try {
    if (themeIsVisible(theme)) {
      await video.play()
      await waitForFirstFrame(video)
    }
  } catch {
    // The poster remains visible when autoplay is temporarily unavailable.
  } finally {
    if (!unmounted) {
      setLoopFading(theme, false)
      resetInProgress[theme] = false
    }
  }
}

function handleTimeUpdate(theme: ThemeVideo) {
  if (!themeIsVisible(theme) || resetInProgress[theme]) return
  const video = videoFor(theme)
  if (!video || !Number.isFinite(video.duration) || video.duration <= 0) return

  const mediaTimeBeforeEnd = playbackRateFor(theme) * props.loopCrossfadeSeconds
  if (video.duration - video.currentTime <= mediaTimeBeforeEnd) {
    void resetLoop(theme)
  }
}

function handleEnded(theme: ThemeVideo) {
  if (themeIsVisible(theme)) void resetLoop(theme)
}

function syncPlaybackWithTheme() {
  isDark.value = document.documentElement.classList.contains('dark')
  notifyIfActivePosterSettled()

  void nextTick(() => {
    configurePlaybackRate(darkVideoRef.value, props.darkPlaybackRate)
    configurePlaybackRate(lightVideoRef.value, props.lightPlaybackRate)
    const activeVideo = isDark.value ? darkVideoRef.value : lightVideoRef.value
    const inactiveVideo = isDark.value ? lightVideoRef.value : darkVideoRef.value
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
  unmounted = true
  themeObserver?.disconnect()
  darkVideoRef.value?.pause()
  lightVideoRef.value?.pause()
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

.theme-video-background__video {
  transition: opacity 900ms ease-in-out, filter 520ms ease;
  will-change: opacity;
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
