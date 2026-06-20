<template>
  <div class="relative w-full h-[600px] flex items-end justify-center perspective-1000">
    <!-- Purple Character -->
    <div
      class="absolute left-[-10%] bottom-0 w-64 h-[500px] bg-[#7C3AED] rounded-t-[40px] shadow-2xl transition-transform duration-300"
      :style="{ transform: `translateY(${purpleY}px) scale(${isLookingAtEachOther ? 1.05 : 1}) rotate(${isLookingAtEachOther ? 5 : 0}deg)` }"
    >
      <div class="absolute top-24 left-1/2 -translate-x-1/2 flex space-x-6">
        <AuthEyeBall
          :size="24"
          :pupil-size="8"
          :max-distance="6"
          :is-blinking="isPurpleBlinking"
          :force-look-x="isLookingAtEachOther ? 10 : (isTyping ? 5 : undefined)"
          :force-look-y="isLookingAtEachOther ? 5 : (isTyping ? 5 : undefined)"
        />
        <AuthEyeBall
          :size="24"
          :pupil-size="8"
          :max-distance="6"
          :is-blinking="isPurpleBlinking"
          :force-look-x="isLookingAtEachOther ? 10 : (isTyping ? 5 : undefined)"
          :force-look-y="isLookingAtEachOther ? 5 : (isTyping ? 5 : undefined)"
        />
      </div>
    </div>

    <!-- Black Character -->
    <div
      class="absolute left-[30%] bottom-0 w-48 h-[350px] bg-[#1F2937] rounded-t-[30px] shadow-2xl z-10 transition-transform duration-300"
      :style="{ transform: `translateY(${blackY}px) scale(${isLookingAtEachOther ? 0.95 : 1}) rotate(${isLookingAtEachOther ? -5 : 0}deg)` }"
    >
      <div class="absolute top-16 left-1/2 -translate-x-1/2 flex space-x-4">
        <AuthEyeBall
          :size="20"
          :pupil-size="6"
          :max-distance="5"
          :is-blinking="isBlackBlinking"
          :force-look-x="isLookingAtEachOther ? -10 : (isTyping ? 8 : undefined)"
          :force-look-y="isLookingAtEachOther ? -5 : (isTyping ? -2 : undefined)"
        />
        <AuthEyeBall
          :size="20"
          :pupil-size="6"
          :max-distance="5"
          :is-blinking="isBlackBlinking"
          :force-look-x="isLookingAtEachOther ? -10 : (isTyping ? 8 : undefined)"
          :force-look-y="isLookingAtEachOther ? -5 : (isTyping ? -2 : undefined)"
        />
      </div>
    </div>

    <!-- Orange Character -->
    <div
      class="absolute left-[5%] bottom-0 w-72 h-[250px] bg-[#FB923C] rounded-t-full shadow-2xl z-20 transition-transform duration-300"
      :style="{ transform: `translateY(${orangeY}px) scale(${isLookingAtEachOther ? 1.02 : 1})` }"
    >
      <div class="absolute top-20 left-1/2 -translate-x-1/2 flex space-x-12">
        <AuthPupil
          :size="10"
          :max-distance="4"
          :force-look-x="isTyping ? 4 : undefined"
          :force-look-y="isTyping ? -4 : undefined"
        />
        <AuthPupil
          :size="10"
          :max-distance="4"
          :force-look-x="isTyping ? 4 : undefined"
          :force-look-y="isTyping ? -4 : undefined"
        />
      </div>
    </div>

    <!-- Yellow Character -->
    <div
      class="absolute right-[0%] bottom-0 w-56 h-[300px] bg-[#FCD34D] rounded-t-[80px] shadow-2xl z-30 transition-transform duration-300"
      :style="{ transform: `translateY(${yellowY}px) scale(${isLookingAtEachOther ? 0.98 : 1}) rotate(${isLookingAtEachOther ? -8 : 0}deg)` }"
    >
      <div class="absolute top-16 left-1/2 -translate-x-1/2 flex space-x-8">
        <AuthEyeBall
          :size="16"
          :pupil-size="6"
          :max-distance="4"
          :is-blinking="isYellowBlinking"
          :force-look-x="isLookingAtEachOther ? -8 : (showPassword ? -5 : (isTyping ? 6 : undefined))"
          :force-look-y="isLookingAtEachOther ? 2 : (showPassword ? 5 : (isTyping ? 2 : undefined))"
        />
        <AuthEyeBall
          :size="16"
          :pupil-size="6"
          :max-distance="4"
          :is-blinking="isYellowBlinking"
          :force-look-x="isLookingAtEachOther ? -8 : (showPassword ? -5 : (isTyping ? 6 : undefined))"
          :force-look-y="isLookingAtEachOther ? 2 : (showPassword ? 5 : (isTyping ? 2 : undefined))"
        />
      </div>
      <!-- Yellow character's mouth -->
      <div class="absolute top-28 left-1/2 -translate-x-1/2 w-16 h-1 bg-black rounded-full transition-all duration-300" :class="{'h-4 rounded-full': showPassword, 'w-8 h-8 rounded-full': isLookingAtEachOther}"></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useAuthInteractionStore } from '@/stores/authInteraction'
import AuthEyeBall from './AuthEyeBall.vue'
import AuthPupil from './AuthPupil.vue'

const authInteraction = useAuthInteractionStore()
const { isTyping, showPassword, passwordLength } = storeToRefs(authInteraction)

const purpleY = ref(0)
const blackY = ref(0)
const orangeY = ref(0)
const yellowY = ref(0)

const isPurpleBlinking = ref(false)
const isBlackBlinking = ref(false)
const isYellowBlinking = ref(false)
const isLookingAtEachOther = ref(false)

let intervals: number[] = []

watch(isTyping, (typing) => {
  if (typing) {
    purpleY.value = 20
    blackY.value = 10
    orangeY.value = 5
    yellowY.value = 15
  } else {
    purpleY.value = 0
    blackY.value = 0
    orangeY.value = 0
    yellowY.value = 0
  }
})

watch(showPassword, (showing) => {
  if (showing) {
    yellowY.value = -20
  } else {
    yellowY.value = isTyping.value ? 15 : 0
  }
})

watch(passwordLength, (len) => {
  if (isTyping.value) {
    const bounce = (len % 3) * 5
    purpleY.value = 20 + bounce
    blackY.value = 10 - bounce
    orangeY.value = 5 + bounce
    yellowY.value = 15 - bounce
  }
})

const blink = (setBlink: (val: boolean) => void) => {
  setBlink(true)
  setTimeout(() => setBlink(false), 150)
}

const randomBlink = () => {
  if (Math.random() > 0.7) blink(isPurpleBlinking.value ? () => {} : (val) => isPurpleBlinking.value = val)
  if (Math.random() > 0.8) blink(isBlackBlinking.value ? () => {} : (val) => isBlackBlinking.value = val)
  if (Math.random() > 0.6) blink(isYellowBlinking.value ? () => {} : (val) => isYellowBlinking.value = val)
}

const randomInteraction = () => {
  if (Math.random() > 0.8 && !isTyping.value && !showPassword.value) {
    isLookingAtEachOther.value = true
    setTimeout(() => {
      isLookingAtEachOther.value = false
    }, 2000)
  }
}

onMounted(() => {
  intervals.push(window.setInterval(randomBlink, 3000))
  intervals.push(window.setInterval(randomInteraction, 8000))
})

onUnmounted(() => {
  intervals.forEach(clearInterval)
})
</script>
