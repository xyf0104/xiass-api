export const adsPowerHelperReleaseBase = 'https://github.com/xyf0104/xiass-api/releases/download/adspower-helper-latest'
export const adsPowerHelperMacDownloadURL = `${adsPowerHelperReleaseBase}/xiass-adspower-helper-macos-universal.dmg`
export const adsPowerHelperWindowsDownloadURL = `${adsPowerHelperReleaseBase}/xiass-adspower-helper-windows-x64.exe`
export const adsPowerHelperHealthURL = 'http://127.0.0.1:34987/healthz'

export const adsPowerHelperUnavailableMessage = '未能连接本机 XIASS AdsPower 助手，或 AdsPower API 未就绪，不代表未安装。已启动时，请检查浏览器的本地网络访问权限，并在“Ads 设置”完成当前站点的常驻助手配对；手机或其他电脑需使用已配对的助手。'

// The helper waits up to five seconds for AdsPower; allow it to finish first.
export async function isAdsPowerHelperAvailable(timeoutMs = 8000): Promise<boolean> {
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
