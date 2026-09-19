export const adsPowerHelperReleaseBase = 'https://github.com/xyf0104/xiass-api/releases/download/adspower-helper-latest'
export const adsPowerHelperMacDownloadURL = `${adsPowerHelperReleaseBase}/xiass-adspower-helper-macos-universal.dmg`
export const adsPowerHelperWindowsDownloadURL = `${adsPowerHelperReleaseBase}/xiass-adspower-helper-windows-x64.exe`
export const adsPowerHelperHealthURL = 'http://127.0.0.1:34987/healthz'

export const adsPowerHelperUnavailableMessage = '未检测到可用的 XIASS AdsPower 助手。请先安装并启动助手与 AdsPower，在“Ads 设置”中填写本机 API Key 和与 XIASS 服务器出口 IP 一致的 SOCKS5/HTTP 节点。'

export async function isAdsPowerHelperAvailable(timeoutMs = 2500): Promise<boolean> {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await fetch(adsPowerHelperHealthURL, {
      method: 'GET',
      mode: 'cors',
      cache: 'no-store',
      credentials: 'omit',
      signal: controller.signal,
    })
    return response.ok
  } catch {
    return false
  } finally {
    window.clearTimeout(timer)
  }
}
