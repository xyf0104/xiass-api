<template>
  <div class="auth-layout relative grid min-h-screen overflow-hidden bg-[#cdd8df] text-gray-900 transition-colors duration-500 dark:bg-[#061720] dark:text-white lg:grid-cols-2">
    <DarkVideoBackground blurred />

    <!-- Canvas for particle background -->
    <canvas ref="canvasRef" class="absolute inset-0 w-full h-full pointer-events-none z-0"></canvas>

    <!-- Left side: Branding and Interactive Characters -->
    <div class="relative z-10 hidden flex-col justify-between bg-transparent p-12 text-gray-900 dark:text-white lg:flex">
      <!-- Logo/Brand (Top Left) -->
      <div class="relative z-20">
        <div class="flex cursor-pointer items-center gap-3 text-lg font-semibold" @click="$router.push('/')">
          <img
            :src="isDark ? '/brand/xiass-mark-dark.png' : '/brand/xiass-mark-light.png'"
            alt="XIASS API"
            class="h-10 w-10 object-contain"
          />
          <span>{{ siteName }}</span>
        </div>
      </div>

      <div class="relative z-20 flex items-end justify-center h-[500px]">
        <AuthCharacters />
      </div>
    </div>

    <!-- Right side: Content Area (Login/Register Form) -->
    <div class="relative z-10 flex w-full items-center justify-center bg-transparent p-8">
      <button
        type="button"
        class="auth-theme-toggle absolute right-6 top-6 flex h-10 w-10 items-center justify-center rounded-xl border border-white/20 bg-white/20 text-gray-800 shadow-lg backdrop-blur-xl transition-colors hover:bg-white/35 dark:text-white"
        :title="isDark ? '切换到浅色模式' : '切换到深色模式'"
        @click="handleThemeToggle"
      >
        <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
      </button>
      <div class="auth-card w-full max-w-[420px] rounded-3xl border border-white/10 bg-white/5 p-8 shadow-2xl backdrop-blur-xl">
        <slot />
        
        <!-- 底部链接 -->
        <div class="mt-4 text-center text-sm">
          <slot name="footer" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 登录页布局 — 赛博朋克科技感
 * 特色：Canvas 粒子连线动画 + 毛玻璃登录卡片 + 渐变发光品牌文字
 */
import { onMounted, onUnmounted, ref } from 'vue'
import { useAppStore } from '@/stores'
import DarkVideoBackground from '@/components/common/DarkVideoBackground.vue'
import AuthCharacters from '@/components/auth/AuthCharacters.vue'
import Icon from '@/components/icons/Icon.vue'
import { getCurrentTheme, toggleTheme } from '@/utils/theme'

const appStore = useAppStore()
const siteName = ref(appStore.siteName || 'XIASS API')
const isDark = ref(getCurrentTheme() === 'dark')

function handleThemeToggle() {
  isDark.value = toggleTheme() === 'dark'
}

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
    const darkTheme = document.documentElement.classList.contains('dark')
    const primaryColor = darkTheme
      ? { r: 255, g: 255, b: 255 }
      : { r: 37, g: 99, b: 235 }
    const lineStrength = darkTheme ? 0.15 : 0.2
    const particleStrength = darkTheme ? 1 : 1.25

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
          const alpha = (1 - dist / connectionDist) * lineStrength
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
      ctx.fillStyle = `rgba(${primaryColor.r}, ${primaryColor.g}, ${primaryColor.b}, ${Math.min(1, p.opacity * particleStrength)})`
      ctx.fill()
    }

    animationId = requestAnimationFrame(animate)
  }

  animate()
}

onMounted(() => {
  void appStore.fetchPublicSettings().then(() => {
    siteName.value = appStore.siteName || 'XIASS API'
  })
  initParticleAnimation()
})

onUnmounted(() => {
  cancelAnimationFrame(animationId)
})
</script>

<style scoped>
/* Remove the gradient glow and border animations to match the clean design */
</style>
