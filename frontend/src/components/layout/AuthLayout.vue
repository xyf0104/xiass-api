<template>
  <div class="auth-layout relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <!-- 深色科技感背景 -->
    <div class="absolute inset-0 bg-gradient-to-br from-[#0a0e1a] via-[#0f1629] to-[#0a0e1a]"></div>

    <!-- 动态粒子网格动画 (Canvas) -->
    <canvas
      ref="canvasRef"
      class="pointer-events-none absolute inset-0 h-full w-full"
    ></canvas>

    <!-- 装饰性光球 -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="absolute -right-20 -top-20 h-[500px] w-[500px] rounded-full bg-primary-500/8 blur-[100px] animate-pulse-slow"></div>
      <div class="absolute -bottom-32 -left-32 h-[400px] w-[400px] rounded-full bg-cyan-500/6 blur-[80px] animate-pulse-slow [animation-delay:2s]"></div>
      <div class="absolute left-1/2 top-1/3 h-64 w-64 -translate-x-1/2 rounded-full bg-primary-400/5 blur-[60px] animate-pulse-slow [animation-delay:4s]"></div>
    </div>

    <!-- 登录内容区 -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/品牌 -->
      <div class="mb-8 text-center">
        <template v-if="settingsLoaded">
          <!-- Logo 带发光边框 -->
          <div class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl bg-gradient-to-br from-primary-500/20 to-cyan-500/20 shadow-lg shadow-primary-500/20 ring-1 ring-primary-500/30 backdrop-blur-sm">
            <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <!-- 品牌名 渐变发光 -->
          <h1 class="text-gradient-glow mb-2 text-3xl font-bold">
            {{ siteName }}
          </h1>
          <p class="text-sm text-gray-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- 登录卡片：毛玻璃 + 微光边框 -->
      <div class="auth-card rounded-2xl border border-white/[0.06] bg-white/[0.03] p-8 shadow-2xl shadow-black/20 backdrop-blur-xl">
        <slot />
      </div>

      <!-- 底部链接 -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- 版权 -->
      <div class="mt-8 text-center text-xs text-gray-600">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 登录页布局 — 赛博朋克科技感
 * 特色：Canvas 粒子连线动画 + 毛玻璃登录卡片 + 渐变发光品牌文字
 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const currentYear = computed(() => new Date().getFullYear())

// ==================== Canvas 粒子动画 ====================

const canvasRef = ref<HTMLCanvasElement | null>(null)
let animationId = 0

interface Particle {
  x: number
  y: number
  vx: number
  vy: number
  radius: number
  opacity: number
}

function initParticleAnimation() {
  const canvas = canvasRef.value
  if (!canvas) return

  const ctx = canvas.getContext('2d')
  if (!ctx) return

  // 高清屏适配
  const dpr = window.devicePixelRatio || 1
  const resize = () => {
    canvas.width = window.innerWidth * dpr
    canvas.height = window.innerHeight * dpr
    canvas.style.width = window.innerWidth + 'px'
    canvas.style.height = window.innerHeight + 'px'
    ctx.scale(dpr, dpr)
  }
  resize()
  window.addEventListener('resize', resize)

  // 生成粒子
  const particleCount = Math.min(80, Math.floor(window.innerWidth / 15))
  const particles: Particle[] = []
  const connectionDist = 150
  const primaryColor = { r: 14, g: 165, b: 233 } // primary-500 色

  for (let i = 0; i < particleCount; i++) {
    particles.push({
      x: Math.random() * window.innerWidth,
      y: Math.random() * window.innerHeight,
      vx: (Math.random() - 0.5) * 0.5,
      vy: (Math.random() - 0.5) * 0.5,
      radius: Math.random() * 1.5 + 0.5,
      opacity: Math.random() * 0.5 + 0.2,
    })
  }

  function animate() {
    if (!ctx || !canvas) return
    const w = window.innerWidth
    const h = window.innerHeight
    ctx.clearRect(0, 0, w, h)

    // 更新粒子位置
    for (const p of particles) {
      p.x += p.vx
      p.y += p.vy

      // 边界反弹
      if (p.x < 0 || p.x > w) p.vx *= -1
      if (p.y < 0 || p.y > h) p.vy *= -1
    }

    // 绘制连线
    for (let i = 0; i < particles.length; i++) {
      for (let j = i + 1; j < particles.length; j++) {
        const dx = particles[i].x - particles[j].x
        const dy = particles[i].y - particles[j].y
        const dist = Math.sqrt(dx * dx + dy * dy)

        if (dist < connectionDist) {
          const alpha = (1 - dist / connectionDist) * 0.15
          ctx.beginPath()
          ctx.strokeStyle = `rgba(${primaryColor.r}, ${primaryColor.g}, ${primaryColor.b}, ${alpha})`
          ctx.lineWidth = 0.5
          ctx.moveTo(particles[i].x, particles[i].y)
          ctx.lineTo(particles[j].x, particles[j].y)
          ctx.stroke()
        }
      }
    }

    // 绘制粒子
    for (const p of particles) {
      ctx.beginPath()
      ctx.arc(p.x, p.y, p.radius, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(${primaryColor.r}, ${primaryColor.g}, ${primaryColor.b}, ${p.opacity})`
      ctx.fill()
    }

    animationId = requestAnimationFrame(animate)
  }

  animate()
}

onMounted(() => {
  appStore.fetchPublicSettings()
  initParticleAnimation()
})

onUnmounted(() => {
  cancelAnimationFrame(animationId)
})
</script>

<style scoped>
/* 品牌名渐变发光 */
.text-gradient-glow {
  background: linear-gradient(135deg, #38bdf8, #0ea5e9, #06b6d4, #38bdf8);
  background-size: 200% 200%;
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  animation: gradient-shift 4s ease infinite;
}

@keyframes gradient-shift {
  0%, 100% { background-position: 0% 50%; }
  50% { background-position: 100% 50%; }
}

/* 登录卡片微光边框动画 */
.auth-card {
  position: relative;
}

.auth-card::before {
  content: '';
  position: absolute;
  inset: -1px;
  border-radius: 1rem;
  padding: 1px;
  background: linear-gradient(
    135deg,
    rgba(14, 165, 233, 0.3),
    transparent 40%,
    transparent 60%,
    rgba(6, 182, 212, 0.2)
  );
  -webkit-mask: linear-gradient(#fff 0 0) content-box, linear-gradient(#fff 0 0);
  -webkit-mask-composite: xor;
  mask-composite: exclude;
  pointer-events: none;
}

/* 慢脉冲动画 */
.animate-pulse-slow {
  animation: pulse-slow 6s ease-in-out infinite;
}

@keyframes pulse-slow {
  0%, 100% { opacity: 0.4; transform: scale(1); }
  50% { opacity: 0.7; transform: scale(1.05); }
}
</style>
