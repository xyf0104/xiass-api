<template>
  <div class="min-h-screen grid lg:grid-cols-2 bg-[#050B14] text-white relative overflow-hidden">
    <!-- Canvas for particle background -->
    <canvas ref="canvasRef" class="absolute inset-0 w-full h-full pointer-events-none z-0"></canvas>

    <!-- Left side: Branding and Interactive Characters -->
    <div class="relative hidden lg:flex flex-col justify-between p-12 text-white z-10 bg-transparent">
      <!-- Logo/Brand (Top Left) -->
      <div class="relative z-20">
        <div class="flex items-center gap-2 text-lg font-semibold cursor-pointer" @click="router.push('/')">
          <!-- User's Logo Icon -->
          <div class="size-8 rounded-lg bg-white/10 backdrop-blur-sm flex items-center justify-center">
            <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" class="size-4">
              <path d="M12 2L2 22H22L12 2Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </div>
          <span>{{ siteName }}</span>
        </div>
      </div>

      <div class="relative z-20 flex items-end justify-center h-[500px]">
        <AuthCharacters />
      </div>

      <!-- Footer links (Bottom Left) -->
      <div class="relative z-20 flex items-center gap-8 text-sm text-gray-400">
        <a href="#" class="hover:text-white transition-colors">Privacy Policy</a>
        <a href="#" class="hover:text-white transition-colors">Terms of Service</a>
        <a href="#" class="hover:text-white transition-colors">Contact</a>
      </div>
    </div>

    <!-- Right side: Content Area (Login/Register Form) -->
    <div class="flex items-center justify-center p-8 bg-transparent z-10 w-full">
      <div class="w-full max-w-[420px] bg-white/5 backdrop-blur-xl p-8 rounded-3xl border border-white/10 shadow-2xl">
        <slot />
        
        <!-- 底部链接 -->
        <div class="mt-4 text-center text-sm">
          <slot name="footer" />
        </div>

        <!-- 版权 -->
        <div class="mt-8 text-center text-xs text-gray-500">
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
