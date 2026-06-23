import json
import re
import os

with open('apikey_docs.json', 'r', encoding='utf-8') as f:
    docs = json.load(f)

# Deduplicate based on title
unique_docs = []
seen = set()
for doc in docs:
    if doc['title'] not in seen:
        seen.add(doc['title'])
        unique_docs.append(doc)

# Replace functions
def clean_html(html):
    # Extract content div
    match = re.search(r'<div[^>]*class="[^"]*flex-1 min-w-0[^"]*"[^>]*>(.*?)</div></div>$', html, re.DOTALL)
    if not match:
        match = re.search(r'<div[^>]*class="[^"]*flex-1 min-w-0[^"]*"[^>]*>(.*)', html, re.DOTALL)
    if match:
        html = match.group(1)
        if html.endswith('</div></div>'):
            html = html[:-12]
            
    # Remove data-v-xxxxxx
    html = re.sub(r'\sdata-v-[a-zA-Z0-9]+=""', '', html)
    
    # Replace domains and names
    html = html.replace('https://apikey.fun', 'https://api.xiass.com')
    html = html.replace('apikey.fun', 'api.xiass.com')
    html = html.replace('APIKEY.FUN', 'NoWind-API')
    
    # Replace custom classes with sub2api tailwind classes
    replacements = {
        'text-on-surface-variant': 'text-gray-600 dark:text-gray-400',
        'text-on-surface': 'text-gray-900 dark:text-gray-100',
        'bg-surface-container-highest': 'bg-gray-200 dark:bg-dark-700',
        'bg-surface-container-low': 'bg-gray-100 dark:bg-dark-800',
        'bg-background-ds': 'bg-gray-50 dark:bg-dark-900',
        'border-outline-variant': 'border-gray-200 dark:border-dark-700',
    }
    for old, new in replacements.items():
        html = html.replace(old, new)
        
    # Some buttons are tabs that switch content inside the HTML. 
    # e.g., <button class="... bg-[#232436] ...">Windows PowerShell</button>
    # Since we extract static HTML, those internal tabs won't work natively without vue logic.
    # But wait, looking at the competitor's HTML, the tabs and content are just ALL listed sequentially if we scraped it?
    # Actually, the competitor's docs don't hide the inactive tabs! They just render all of them sequentially: "Windows PowerShell", then "macOS / Linux / WSL". 
    # Wait, earlier I found both "Windows" and "macOS" in the same HTML chunk. Yes, they just stack them or use native browser CSS to show them.
    # So the static HTML is fully sufficient!
    
    return html

cleaned_docs = []
for doc in unique_docs:
    cleaned_docs.append({
        'title': doc['title'],
        'html': clean_html(doc['html'])
    })

vue_template = """<template>
  <div class="min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white">
    <header class="border-b border-gray-200 bg-white/95 dark:border-dark-800 dark:bg-dark-900/95">
      <div class="mx-auto flex max-w-7xl items-center justify-between gap-4 px-4 py-4 sm:px-6">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-3">
          <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center overflow-hidden rounded-xl bg-white shadow-sm ring-1 ring-gray-200 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-semibold text-gray-950 dark:text-white">
            {{ siteName }}
          </span>
        </RouterLink>
        <RouterLink
          to="/login"
          class="inline-flex flex-shrink-0 items-center justify-center rounded-lg bg-primary-600 px-4 py-2 text-sm font-semibold text-white shadow-sm shadow-primary-600/20 transition hover:bg-primary-700"
        >
          {{ t('home.login') }}
        </RouterLink>
      </div>
    </header>

    <main class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:py-10">
      <div v-if="loading" class="flex min-h-[320px] items-center justify-center">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else class="flex flex-col md:flex-row gap-10 lg:gap-16">
        <!-- Sidebar -->
        <aside class="md:w-64 shrink-0 mt-4 md:mt-0 order-first">
          <div class="sticky top-8 space-y-1">
            <p class="text-sm font-bold tracking-wide text-gray-500 mb-5 px-3">快速上手</p>
            <nav class="flex flex-col gap-1">
              <button 
                v-for="(doc, idx) in docsList" 
                :key="idx" 
                @click="activeDoc = idx"
                class="text-left px-4 py-3 rounded-xl text-sm font-bold transition-all duration-300"
                :class="activeDoc === idx ? 'bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-300' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-800'"
              >
                {{ doc.title }}
              </button>
            </nav>
          </div>
        </aside>

        <!-- Content -->
        <div class="flex-1 min-w-0" @click="handleContentClick">
"""

for idx, doc in enumerate(cleaned_docs):
    vue_template += f'          <div v-show="activeDoc === {idx}">\n'
    vue_template += f'            {doc["html"]}\n'
    vue_template += f'          </div>\n'

vue_template += """        </div>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPublicSettings } from '@/api/auth'
import { sanitizeUrl } from '@/utils/url'
import type { PublicSettings } from '@/types'

const { t } = useI18n()
const settings = ref<PublicSettings | null>(null)
const loading = ref(true)

const activeDoc = ref(0)
const docsList = [
"""

for doc in cleaned_docs:
    vue_template += f"  {{ title: '{doc['title']}' }},\n"

vue_template += """]

const siteName = computed(() => settings.value?.site_name || 'No Wind API')
const siteLogo = computed(() => sanitizeUrl(settings.value?.site_logo || '', {
  allowRelative: true,
  allowDataUrl: true,
}))

const handleContentClick = async (e: MouseEvent) => {
  const target = e.target as HTMLElement;
  if (target.tagName === 'BUTTON' && target.innerText === '复制') {
    const container = target.closest('div.bg-\\[\\#1C1D2C\\]') || target.closest('.rounded-xl');
    const pre = container?.querySelector('pre') || container?.querySelector('code');
    if (pre) {
      try {
        await navigator.clipboard.writeText(pre.innerText);
        const originalText = target.innerText;
        target.innerText = '已复制';
        setTimeout(() => { target.innerText = originalText; }, 2000);
      } catch (err) {
        console.error('Failed to copy: ', err);
      }
    }
  }
};

onMounted(async () => {
  loading.value = true
  try {
    settings.value = await getPublicSettings()
  } catch {
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
/* Optional: any specific scoped styles for syntax highlighting if missing */
</style>
"""

with open('frontend/src/views/public/DocsView.vue', 'w', encoding='utf-8') as f:
    f.write(vue_template)

print("Generated frontend/src/views/public/DocsView.vue")
