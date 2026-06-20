<template>
  <div class="auth-layout relative flex min-h-screen items-center justify-center overflow-hidden p-4 bg-[#0B0D17]">
    
    <!-- Top Left Brand -->
    <div class="absolute top-8 left-8 z-50 flex items-center gap-3">
      <div class="flex h-8 w-8 items-center justify-center rounded bg-white">
        <svg class="h-5 w-5 text-black" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 2L2 22h20L12 2zm0 4.5l6.5 13h-13L12 6.5z"/>
        </svg>
      </div>
      <span class="text-xl font-semibold text-white tracking-wide">
        {{ siteName }}
      </span>
    </div>

    <!-- 动态粒子网格动画 (Canvas) -->
    <canvas
      ref="canvasRef"
      class="pointer-events-none absolute inset-0 h-full w-full opacity-60"
    ></canvas>

    <!-- 登录内容区 (2 Column Layout on large screens) -->
    <div class="relative z-10 w-full max-w-[1200px] grid lg:grid-cols-2 gap-12 items-center justify-between">
      
      <!-- 左侧：卡通人物动画 -->
      <div class="hidden lg:flex justify-center items-end h-[600px] relative pointer-events-none">
        <AuthCharacters class="scale-90 xl:scale-100 origin-bottom" />
      </div>

      <!-- 右侧：登录表单卡片 -->
      <div class="w-full max-w-md mx-auto lg:ml-auto lg:mr-8">
        <!-- 登录卡片：极简深色卡片 -->
        <div class="auth-card rounded-3xl bg-[#13151A] p-10 shadow-2xl">
          <slot />
        </div>

        <!-- 底部链接 -->
        <div class="mt-4 text-center text-sm">
          <slot name="footer" />
        </div>

        <!-- 版权 -->
        <div class="mt-12 text-center text-xs text-gray-600">
          &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
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
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useAppStore } from '@/stores'
import AuthCharacters from '@/components/auth/AuthCharacters.vue'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
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
/* Remove the gradient glow and border animations to match the clean design */
</style>
