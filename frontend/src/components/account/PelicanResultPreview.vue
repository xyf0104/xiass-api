<template>
  <div class="preview-shell" data-testid="result-thumbnail">
    <iframe v-if="preview" :srcdoc="preview" sandbox="allow-scripts" referrerpolicy="no-referrer" :title="title" data-testid="result-preview" />
    <span v-else role="status" class="text-xs text-gray-500">{{ failed ? t('admin.accounts.pelicanBenchmark.resultFailed') : t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPelicanResult } from '@/api/admin/pelicanBenchmark'

const props = defineProps<{ id: string; title: string }>()
const { t } = useI18n()
const preview = ref('')
const failed = ref(false)
let request: AbortController | undefined
watch(() => props.id, async id => {
  request?.abort()
  const controller = new AbortController()
  request = controller
  preview.value = ''
  failed.value = false
  try {
    const result = await getPelicanResult(id, controller.signal)
    if (controller.signal.aborted) return
    if (result.id !== id || result.status !== 'succeeded' || !result.html?.trim()) throw new Error('Invalid result')
    // Generated scripts remain in an opaque origin; no network or parent access.
    const policy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none';"
    // Keep the layout viewport fixed, including vh/vw. Scale the entire document,
    // not the iframe dimensions; measure without the previous transform each time.
    const sizing = `<script>addEventListener('load',()=>{
      const root=document.documentElement;
      let scheduled=false;
      const report=()=>{
        scheduled=false;
        root.style.setProperty('transform','none','important');
        root.style.setProperty('transform-origin','0 0','important');
        root.style.setProperty('overflow','hidden','important');
        const width=Math.max(1100,root.scrollWidth,document.body?.scrollWidth||0);
        const height=Math.max(690,root.scrollHeight,document.body?.scrollHeight||0);
        const scale=Math.min(1100/width,690/height);
        root.style.setProperty('transform','translate('+((1100-width*scale)/2)+'px,'+((690-height*scale)/2)+'px) scale('+scale+')','important');
      };
      const schedule=()=>{if(!scheduled){scheduled=true;requestAnimationFrame(report)}};
      report();
      const observer=new ResizeObserver(schedule);
      observer.observe(root);if(document.body)observer.observe(document.body);
      new MutationObserver(schedule).observe(root,{subtree:true,childList:true,characterData:true});
      root.addEventListener('load',schedule,true);
      document.fonts?.ready.then(schedule);
    });<\/script>`
    preview.value = `<meta http-equiv="Content-Security-Policy" content="${policy}">${result.html}${sizing}`
  } catch {
    if (!controller.signal.aborted) failed.value = true
  }
}, { immediate: true })
onUnmounted(() => {
  request?.abort()
})
</script>

<style scoped>
.preview-shell { width: 220px; height: 138px; flex-shrink: 0; overflow: hidden; position: relative; display: grid; place-items: center; outline: 1px solid #64748b; border-radius: 6px; background: white; }
iframe { position: absolute; top: 0; left: 0; width: 1100px; height: 690px; border: 0; transform: scale(.2); transform-origin: top left; }
</style>
