<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useTemplateRef, nextTick } from 'vue'

const props = withDefaults(defineProps<{
  content?: string
  trigger?: 'hover' | 'click'
  widthClass?: string
}>(), {
  trigger: 'hover',
  widthClass: 'w-64',
})

const show = ref(false)
const triggerRef = useTemplateRef<HTMLElement>('trigger')
const tooltipRef = useTemplateRef<HTMLElement>('tooltip')
const tooltipStyle = ref({
  top: '0px',
  left: '0px',
  maxWidth: 'calc(100vw - 1rem)',
})
const tooltipPlacement = ref<'top' | 'bottom'>('top')
const viewportMargin = 8

function getViewportBounds() {
  const viewport = window.visualViewport
  const left = viewport?.offsetLeft ?? 0
  const top = viewport?.offsetTop ?? 0
  const width = viewport?.width ?? window.innerWidth
  const height = viewport?.height ?? window.innerHeight

  return {
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
  }
}

function constrainTooltipWidth() {
  const viewport = getViewportBounds()
  tooltipStyle.value = {
    ...tooltipStyle.value,
    maxWidth: `${Math.max(0, viewport.width - viewportMargin * 2)}px`,
  }
}

function openTooltip() {
  constrainTooltipWidth()
  show.value = true
  nextTick(updatePosition)
}

function closeTooltip() {
  show.value = false
}

function onEnter() {
  if (props.trigger !== 'hover') return
  openTooltip()
}

function isInside(container: HTMLElement | null, target: EventTarget | null): boolean {
  return target instanceof Node && !!container?.contains(target)
}

// Keep hover content open while the pointer moves between the trigger and
// the teleported tooltip so text remains selectable and copyable.
function onLeave(event: MouseEvent) {
  if (props.trigger !== 'hover') return
  if (isInside(tooltipRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onTooltipLeave(event: MouseEvent) {
  if (props.trigger !== 'hover') return
  if (isInside(triggerRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onClick(event: MouseEvent) {
  if (props.trigger !== 'click') return
  event.stopPropagation()
  if (show.value) {
    closeTooltip()
    return
  }
  openTooltip()
}

function onDocumentClick(event: MouseEvent) {
  if (props.trigger !== 'click' || !show.value) return
  const target = event.target as Node | null
  if (!target) return
  if (triggerRef.value?.contains(target) || tooltipRef.value?.contains(target)) return
  closeTooltip()
}

function onDocumentKeydown(event: KeyboardEvent) {
  if (props.trigger !== 'click') return
  if (event.key === 'Escape') {
    closeTooltip()
  }
}

function onViewportChange() {
  if (!show.value) return
  constrainTooltipWidth()
  nextTick(updatePosition)
}

function updatePosition() {
  const el = triggerRef.value
  if (!el) return

  const rect = el.getBoundingClientRect()
  const tooltip = tooltipRef.value
  const tooltipWidth = tooltip?.offsetWidth || 256
  const tooltipHeight = tooltip?.offsetHeight || 0
  const viewport = getViewportBounds()
  const centeredLeft = rect.left + rect.width / 2
  const minimumCenter = viewport.left + tooltipWidth / 2 + viewportMargin
  const maximumCenter = viewport.right - tooltipWidth / 2 - viewportMargin
  const left = minimumCenter <= maximumCenter
    ? Math.min(Math.max(centeredLeft, minimumCenter), maximumCenter)
    : viewport.left + viewport.width / 2
  const hasRoomAbove = rect.top - viewport.top >= tooltipHeight + viewportMargin

  tooltipPlacement.value = hasRoomAbove ? 'top' : 'bottom'
  tooltipStyle.value = {
    ...tooltipStyle.value,
    top: hasRoomAbove ? `${rect.top - viewportMargin}px` : `${rect.bottom + viewportMargin}px`,
    left: `${left}px`,
  }
}

onMounted(() => {
  document.addEventListener('click', onDocumentClick, true)
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
  window.visualViewport?.addEventListener('resize', onViewportChange)
  window.visualViewport?.addEventListener('scroll', onViewportChange)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentClick, true)
  document.removeEventListener('keydown', onDocumentKeydown)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
  window.visualViewport?.removeEventListener('resize', onViewportChange)
  window.visualViewport?.removeEventListener('scroll', onViewportChange)
})
</script>

<template>
  <div
    ref="trigger"
    class="group relative ml-1 inline-flex items-center align-middle"
    @mouseenter="onEnter"
    @mouseleave="onLeave"
    @click="onClick"
  >
    <!-- Trigger Icon -->
    <slot name="trigger">
      <svg
        class="h-4 w-4 cursor-help text-gray-400 transition-colors hover:text-primary-600 dark:text-gray-500 dark:hover:text-primary-400"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        stroke-width="2"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
        />
      </svg>
    </slot>

    <!-- Teleport to body to escape modal overflow clipping -->
    <Teleport to="body">
      <div
        ref="tooltip"
        v-show="show"
        role="tooltip"
        :class="[
          'help-tooltip-surface fixed z-[100000100] -translate-x-1/2 rounded-lg p-3 text-xs leading-relaxed text-white shadow-xl ring-1 ring-white/10 before:absolute before:inset-x-0 before:h-3',
          tooltipPlacement === 'top'
            ? '-translate-y-full before:top-full'
            : 'before:bottom-full',
          props.widthClass,
        ]"
        :style="tooltipStyle"
        @mouseleave="onTooltipLeave"
      >
        <button
          v-if="props.trigger === 'click'"
          type="button"
          class="absolute right-1.5 top-1.5 rounded p-1 text-gray-300 transition-colors hover:bg-white/10 hover:text-white"
          aria-label="Close"
          @click.stop="closeTooltip"
        >
          <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
        <slot>{{ content }}</slot>
        <div
          :class="[
            'help-tooltip-arrow absolute left-1/2 h-2 w-2 -translate-x-1/2 rotate-45',
            tooltipPlacement === 'top' ? '-bottom-1' : '-top-1',
          ]"
        ></div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.help-tooltip-surface {
  --help-tooltip-background: rgb(17 24 39);
  background-color: var(--help-tooltip-background) !important;
  backdrop-filter: none !important;
  -webkit-backdrop-filter: none !important;
}

:global(.dark) .help-tooltip-surface {
  --help-tooltip-background: rgb(31 41 55);
}

:global(html:not(.dark) body:has(.app-layout)) .help-tooltip-surface,
:global(.dark body:has(.app-layout)) .help-tooltip-surface {
  background-color: var(--help-tooltip-background) !important;
  border-color: rgb(255 255 255 / 0.12) !important;
  backdrop-filter: none !important;
  -webkit-backdrop-filter: none !important;
}

.help-tooltip-arrow {
  background-color: var(--help-tooltip-background) !important;
}
</style>
