<template>
  <BaseDialog :show="show" title="Ads 指纹浏览器设置" width="wide" @close="$emit('close')">
    <div class="space-y-4 text-sm text-gray-700 dark:text-gray-200" data-testid="adspower-helper-setup-dialog">
      <div class="rounded-md border border-sky-200 bg-sky-50/80 px-4 py-3 text-sky-800 dark:border-sky-900/60 dark:bg-sky-950/25 dark:text-sky-200">
        内置浏览器不需要安装任何程序，默认即可授权。只有选择 Ads 指纹浏览器时才需要完成下面的本机设置。
      </div>

      <section class="adspower-setup-section">
        <span class="adspower-setup-step">1</span>
        <div class="min-w-0">
          <h3 class="font-semibold text-gray-950 dark:text-white">准备 AdsPower API</h3>
          <p class="mt-1 text-gray-500 dark:text-gray-400">打开 AdsPower 的“API &amp; MCP”，启用本地 API；如开启安全校验，复制页面中的 API Key。</p>
        </div>
      </section>

      <section class="adspower-setup-section">
        <span class="adspower-setup-step">2</span>
        <div class="min-w-0 flex-1">
          <h3 class="font-semibold text-gray-950 dark:text-white">安装 XIASS AdsPower 助手</h3>
          <p class="mt-1 text-gray-500 dark:text-gray-400">助手安装后随电脑登录自动启动并在后台常驻，负责接收 XIASS 任务、创建固定环境、关闭 WebRTC、接收 OAuth 回调和调用 AdsPower。</p>
          <div class="mt-3 flex flex-wrap gap-2">
            <a class="btn btn-secondary btn-sm" :href="macDownloadURL" data-testid="adspower-helper-download-macos">
              <Icon name="download" size="sm" />macOS 下载
            </a>
            <a class="btn btn-secondary btn-sm" :href="windowsDownloadURL" data-testid="adspower-helper-download-windows">
              <Icon name="download" size="sm" />Windows 下载
            </a>
          </div>
        </div>
      </section>

      <section class="adspower-setup-section">
        <span class="adspower-setup-step">3</span>
        <div class="min-w-0 flex-1">
          <h3 class="font-semibold text-gray-950 dark:text-white">填写本机配置并检测出口</h3>
          <p class="mt-1 text-gray-500 dark:text-gray-400">先运行 AdsPower 和 XIASS 助手，再打开本机设置页。粘贴 API Key，填写 SOCKS5 主机、端口、账号和密码；保存时会真实检测代理出口 IP。</p>
          <dl class="mt-3 grid gap-2 rounded-md border border-gray-200 bg-white/30 p-3 text-xs dark:border-dark-600 dark:bg-dark-900/20 sm:grid-cols-[7rem_minmax(0,1fr)]">
            <dt class="text-gray-500 dark:text-gray-400">XIASS 地址</dt>
            <dd class="break-all font-mono text-gray-800 dark:text-gray-200">{{ serverOrigin }}</dd>
            <dt class="text-gray-500 dark:text-gray-400">节点名称</dt>
            <dd class="break-all font-mono text-gray-800 dark:text-gray-200">{{ environmentKey }}</dd>
          </dl>
          <button type="button" class="btn btn-primary mt-3" data-testid="open-adspower-helper-setup" @click="openLocalSetup">
            <Icon name="cog" size="sm" />打开本机设置
          </button>
        </div>
      </section>

      <section class="adspower-setup-section">
        <span class="adspower-setup-step">4</span>
        <div class="min-w-0 flex-1">
          <h3 class="font-semibold text-gray-950 dark:text-white">配对常驻助手</h3>
          <p class="mt-1 text-gray-500 dark:text-gray-400">在安装助手的电脑上完成一次配对。以后从手机或其他电脑点击 Ads 授权，任务会由 XIASS 服务器安全下发到这台电脑，不再打开 127.0.0.1。</p>
          <button type="button" class="btn btn-primary mt-3" data-testid="pair-adspower-helper" :disabled="pairing" @click="pairResidentHelper">
            <Icon name="link" size="sm" />{{ pairing ? '正在配对' : '配对常驻助手' }}
          </button>
          <p v-if="pairingError" class="mt-2 text-xs text-red-600 dark:text-red-300">{{ pairingError }}</p>
        </div>
      </section>

      <p class="border-t border-gray-200 pt-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:text-gray-400">
        AdsPower API Key、SOCKS5 账号密码、浏览器 Cookie 和 OAuth Token 均保存在管理员电脑本地；XIASS 服务器只保存账号对应的设备、环境和已验证出口信息。
      </p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adsPowerAPI } from '@/api/admin/adspower'
import { adsPowerHelperMacDownloadURL, adsPowerHelperWindowsDownloadURL } from '@/utils/adspowerHelper'

const props = defineProps<{
  show: boolean
  serverOrigin: string
  environmentKey: string
}>()

defineEmits<{ close: [] }>()

const macDownloadURL = adsPowerHelperMacDownloadURL
const windowsDownloadURL = adsPowerHelperWindowsDownloadURL
const pairing = ref(false)
const pairingError = ref('')
const setupURL = computed(() => {
  const query = new URLSearchParams({ server: props.serverOrigin, environment: props.environmentKey })
  return `http://127.0.0.1:34987/setup?${query.toString()}`
})

function openLocalSetup() {
  const helperWindow = window.open(setupURL.value, '_blank')
  if (helperWindow) helperWindow.opener = null
  else window.location.assign(setupURL.value)
}

async function pairResidentHelper() {
  pairing.value = true
  pairingError.value = ''
  try {
    const result = await adsPowerAPI.pairHelper(props.environmentKey)
    const helperWindow = window.open(result.helper_url, '_blank')
    if (helperWindow) helperWindow.opener = null
    else window.location.assign(result.helper_url)
  } catch (error) {
    pairingError.value = error instanceof Error ? error.message : '无法创建常驻助手配对。'
  } finally {
    pairing.value = false
  }
}
</script>

<style scoped>
.adspower-setup-section {
  display: flex;
  gap: 0.8rem;
  border: 1px solid rgb(148 163 184 / 0.24);
  border-radius: 7px;
  background: rgb(255 255 255 / 0.22);
  padding: 0.9rem;
}

:global(.dark .adspower-setup-section) {
  border-color: rgb(123 178 199 / 0.2);
  background: rgb(3 25 40 / 0.46);
}

.adspower-setup-step {
  display: inline-flex;
  width: 1.8rem;
  height: 1.8rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  background: rgb(14 165 233 / 0.13);
  color: rgb(3 105 161);
  font-weight: 800;
}

:global(.dark .adspower-setup-step) {
  background: rgb(8 145 178 / 0.22);
  color: rgb(165 243 252);
}
</style>
