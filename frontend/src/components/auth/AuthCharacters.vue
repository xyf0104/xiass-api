<template>
  <div class="relative w-[550px] h-[400px]">
    <!-- Purple tall rectangle character -->
    <div 
      class="absolute bottom-0 transition-all duration-700 ease-in-out z-10"
      :style="{
        left: '70px',
        width: '180px',
        height: (isTyping || (passwordLength > 0 && !showPassword)) ? '440px' : '400px',
        backgroundColor: '#6C3FF5',
        borderRadius: '10px 10px 0 0',
        transform: (passwordLength > 0 && showPassword)
          ? `skewX(0deg)`
          : (isTyping || (passwordLength > 0 && !showPassword))
            ? `skewX(${(purplePos.bodySkew || 0) - 12}deg) translateX(40px)` 
            : `skewX(${purplePos.bodySkew || 0}deg)`,
        transformOrigin: 'bottom center',
      }"
    >
      <!-- Eyes -->
      <div 
        class="absolute flex gap-8 transition-all duration-700 ease-in-out"
        :style="{
          left: (passwordLength > 0 && showPassword) ? `${20}px` : isLookingAtEachOther ? `${55}px` : `${45 + purplePos.faceX}px`,
          top: (passwordLength > 0 && showPassword) ? `${35}px` : isLookingAtEachOther ? `${65}px` : `${40 + purplePos.faceY}px`,
        }"
      >
        <AuthEyeBall 
          :size="18" 
          :pupil-size="7" 
          :max-distance="5" 
          eye-color="white" 
          pupil-color="#2D2D2D" 
          :is-blinking="isPurpleBlinking"
          :force-look-x="(passwordLength > 0 && showPassword) ? (isPurplePeeking ? 4 : -4) : isLookingAtEachOther ? 3 : undefined"
          :force-look-y="(passwordLength > 0 && showPassword) ? (isPurplePeeking ? 5 : -4) : isLookingAtEachOther ? 4 : undefined"
        />
        <AuthEyeBall 
          :size="18" 
          :pupil-size="7" 
          :max-distance="5" 
          eye-color="white" 
          pupil-color="#2D2D2D" 
          :is-blinking="isPurpleBlinking"
          :force-look-x="(passwordLength > 0 && showPassword) ? (isPurplePeeking ? 4 : -4) : isLookingAtEachOther ? 3 : undefined"
          :force-look-y="(passwordLength > 0 && showPassword) ? (isPurplePeeking ? 5 : -4) : isLookingAtEachOther ? 4 : undefined"
        />
      </div>
    </div>

    <!-- Black tall rectangle character -->
    <div 
      class="absolute bottom-0 transition-all duration-700 ease-in-out z-20"
      :style="{
        left: '240px',
        width: '120px',
        height: '310px',
        backgroundColor: '#2D2D2D',
        borderRadius: '8px 8px 0 0',
        transform: (passwordLength > 0 && showPassword)
          ? `skewX(0deg)`
          : isLookingAtEachOther
            ? `skewX(${(blackPos.bodySkew || 0) * 1.5 + 10}deg) translateX(20px)`
            : (isTyping || (passwordLength > 0 && !showPassword))
              ? `skewX(${(blackPos.bodySkew || 0) * 1.5}deg)` 
              : `skewX(${blackPos.bodySkew || 0}deg)`,
        transformOrigin: 'bottom center',
      }"
    >
      <!-- Eyes -->
      <div 
        class="absolute flex gap-6 transition-all duration-700 ease-in-out"
        :style="{
          left: (passwordLength > 0 && showPassword) ? `${10}px` : isLookingAtEachOther ? `${32}px` : `${26 + blackPos.faceX}px`,
          top: (passwordLength > 0 && showPassword) ? `${28}px` : isLookingAtEachOther ? `${12}px` : `${32 + blackPos.faceY}px`,
        }"
      >
        <AuthEyeBall 
          :size="16" 
          :pupil-size="6" 
          :max-distance="4" 
          eye-color="white" 
          pupil-color="#2D2D2D" 
          :is-blinking="isBlackBlinking"
          :force-look-x="(passwordLength > 0 && showPassword) ? -4 : isLookingAtEachOther ? 0 : undefined"
          :force-look-y="(passwordLength > 0 && showPassword) ? -4 : isLookingAtEachOther ? -4 : undefined"
        />
        <AuthEyeBall 
          :size="16" 
          :pupil-size="6" 
          :max-distance="4" 
          eye-color="white" 
          pupil-color="#2D2D2D" 
          :is-blinking="isBlackBlinking"
          :force-look-x="(passwordLength > 0 && showPassword) ? -4 : isLookingAtEachOther ? 0 : undefined"
          :force-look-y="(passwordLength > 0 && showPassword) ? -4 : isLookingAtEachOther ? -4 : undefined"
        />
      </div>
    </div>

    <!-- Orange semi-circle character -->
    <div 
      class="absolute bottom-0 transition-all duration-700 ease-in-out z-30"
      :style="{
        left: '0px',
        width: '240px',
        height: '200px',
        backgroundColor: '#FF9B6B',
        borderRadius: '120px 120px 0 0',
        transform: (passwordLength > 0 && showPassword) ? `skewX(0deg)` : `skewX(${orangePos.bodySkew || 0}deg)`,
        transformOrigin: 'bottom center',
      }"
    >
      <!-- Eyes -->
      <div 
        class="absolute flex gap-8 transition-all duration-200 ease-out"
        :style="{
          left: (passwordLength > 0 && showPassword) ? `${50}px` : `${82 + (orangePos.faceX || 0)}px`,
          top: (passwordLength > 0 && showPassword) ? `${85}px` : `${90 + (orangePos.faceY || 0)}px`,
        }"
      >
        <AuthPupil :size="12" :max-distance="5" pupil-color="#2D2D2D" :force-look-x="(passwordLength > 0 && showPassword) ? -5 : undefined" :force-look-y="(passwordLength > 0 && showPassword) ? -4 : undefined" />
        <AuthPupil :size="12" :max-distance="5" pupil-color="#2D2D2D" :force-look-x="(passwordLength > 0 && showPassword) ? -5 : undefined" :force-look-y="(passwordLength > 0 && showPassword) ? -4 : undefined" />
      </div>
    </div>

    <!-- Yellow tall rectangle character -->
    <div 
      class="absolute bottom-0 transition-all duration-700 ease-in-out z-40"
      :style="{
        left: '310px',
        width: '140px',
        height: '230px',
        backgroundColor: '#E8D754',
        borderRadius: '70px 70px 0 0',
        transform: (passwordLength > 0 && showPassword) ? `skewX(0deg)` : `skewX(${yellowPos.bodySkew || 0}deg)`,
        transformOrigin: 'bottom center',
      }"
    >
      <!-- Eyes -->
      <div 
        class="absolute flex gap-6 transition-all duration-200 ease-out"
        :style="{
          left: (passwordLength > 0 && showPassword) ? `${20}px` : `${52 + (yellowPos.faceX || 0)}px`,
          top: (passwordLength > 0 && showPassword) ? `${35}px` : `${40 + (yellowPos.faceY || 0)}px`,
        }"
      >
        <AuthPupil :size="12" :max-distance="5" pupil-color="#2D2D2D" :force-look-x="(passwordLength > 0 && showPassword) ? -5 : undefined" :force-look-y="(passwordLength > 0 && showPassword) ? -4 : undefined" />
        <AuthPupil :size="12" :max-distance="5" pupil-color="#2D2D2D" :force-look-x="(passwordLength > 0 && showPassword) ? -5 : undefined" :force-look-y="(passwordLength > 0 && showPassword) ? -4 : undefined" />
      </div>
      <!-- Mouth -->
      <div 
        class="absolute h-[4px] bg-[#2D2D2D] rounded-full transition-all duration-200 ease-out"
        :style="{
          width: '80px',
          left: (passwordLength > 0 && showPassword) ? `${10}px` : `${40 + (yellowPos.faceX || 0)}px`,
          top: (passwordLength > 0 && showPassword) ? `${88}px` : `${88 + (yellowPos.faceY || 0)}px`,
        }"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useAuthInteractionStore } from '@/stores/authInteraction'
import AuthEyeBall from './AuthEyeBall.vue'
import AuthPupil from './AuthPupil.vue'

const authInteraction = useAuthInteractionStore()
const { isTyping, showPassword, passwordLength } = storeToRefs(authInteraction)

const mouseX = ref(0)
const mouseY = ref(0)

const isPurpleBlinking = ref(false)
const isBlackBlinking = ref(false)
const isPurplePeeking = ref(false)
const isLookingAtEachOther = ref(false)

let intervals: number[] = []
let timeouts: number[] = []

const handleMouseMove = (e: MouseEvent) => {
  mouseX.value = e.clientX
  mouseY.value = e.clientY
}

const calculatePosition = (centerX: number, centerY: number) => {
  const deltaX = mouseX.value - centerX
  const deltaY = mouseY.value - centerY
  
  const faceX = Math.max(-15, Math.min(15, deltaX / 20))
  const faceY = Math.max(-10, Math.min(10, deltaY / 30))
  const bodySkew = Math.max(-6, Math.min(6, -deltaX / 120))
  
  return { faceX, faceY, bodySkew }
}

const purplePos = computed(() => calculatePosition(window.innerWidth * 0.25 + 70 + 90, window.innerHeight / 2 + 100))
const blackPos = computed(() => calculatePosition(window.innerWidth * 0.25 + 240 + 60, window.innerHeight / 2 + 150))
const orangePos = computed(() => calculatePosition(window.innerWidth * 0.25 + 120, window.innerHeight / 2 + 200))
const yellowPos = computed(() => calculatePosition(window.innerWidth * 0.25 + 310 + 70, window.innerHeight / 2 + 180))

// Blinking logic
const scheduleBlink = (setBlinking: (val: boolean) => void) => {
  const blinkTimeout = window.setTimeout(() => {
    setBlinking(true)
    timeouts.push(window.setTimeout(() => {
      setBlinking(false)
      scheduleBlink(setBlinking)
    }, 150))
  }, Math.random() * 4000 + 3000)
  timeouts.push(blinkTimeout)
}

// Looking at each other
watch(isTyping, (typing) => {
  if (typing) {
    isLookingAtEachOther.value = true
    timeouts.push(window.setTimeout(() => {
      isLookingAtEachOther.value = false
    }, 800))
  } else {
    isLookingAtEachOther.value = false
  }
})

// Purple sneaking peek
watch([passwordLength, showPassword], ([len, show]) => {
  if (len > 0 && show) {
    const peekInterval = window.setTimeout(() => {
      isPurplePeeking.value = true
      timeouts.push(window.setTimeout(() => {
        isPurplePeeking.value = false
      }, 800))
    }, Math.random() * 3000 + 2000)
    timeouts.push(peekInterval)
  } else {
    isPurplePeeking.value = false
  }
})

onMounted(() => {
  window.addEventListener("mousemove", handleMouseMove)
  scheduleBlink((val) => isPurpleBlinking.value = val)
  scheduleBlink((val) => isBlackBlinking.value = val)
})

onUnmounted(() => {
  window.removeEventListener("mousemove", handleMouseMove)
  intervals.forEach(clearInterval)
  timeouts.forEach(clearTimeout)
})
</script>
