let runtimePromise: Promise<string> | undefined

function loadThreeRuntime(): Promise<string> {
  // Import source text, never the executable library, into the application.
  // Three's pinned standalone CommonJS build has no external dependencies.
  runtimePromise ??= import('../../node_modules/three/build/three.cjs?raw')
    .then(({ default: source }) => `(function(){const exports={};\n${source}\nwindow.THREE=exports;})();`)
    .catch(error => {
      runtimePromise = undefined
      throw error
    })
  return runtimePromise
}

function isThreeLibraryURL(value: string): boolean {
  try {
    const url = new URL(value, 'https://preview.invalid/')
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.port) return false
    if (url.hostname === 'unpkg.com' || url.hostname === 'cdn.jsdelivr.net') {
      const prefix = url.hostname === 'cdn.jsdelivr.net' ? '/npm' : ''
      return new RegExp(`^${prefix}/three(?:@(?:[0-9]+\\.[0-9]+\\.[0-9]+|latest))?/build/three(?:\\.min)?\\.js$`).test(url.pathname)
    }
    return url.hostname === 'cdnjs.cloudflare.com'
      && /^\/ajax\/libs\/three\.js\/(?:r?\d+|\d+\.\d+\.\d+)\/three(?:\.min)?\.js$/.test(url.pathname)
  } catch {
    return false
  }
}

function isExecutableScript(script: HTMLScriptElement): boolean {
  const type = script.type.trim().toLowerCase()
  return !type || ['text/javascript', 'application/javascript', 'module'].includes(type)
}

/** Prepare inert source for a sandboxed preview. Ordinary 2D HTML is unchanged. */
export async function preparePelicanHTML(html: string): Promise<string> {
  if (!/\bTHREE\b|three(?:\.min)?\.js/.test(html)) return html
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  const scripts = [...parsed.querySelectorAll<HTMLScriptElement>('script')]
  const libraryScripts = scripts.filter(script => script.hasAttribute('src') && isThreeLibraryURL(script.getAttribute('src')!))
  const usesThree = scripts.some(script => isExecutableScript(script)
    && !['src', 'href', 'xlink:href'].some(name => script.hasAttribute(name))
    && /\bTHREE\b/.test(script.textContent || ''))
  if (!usesThree && !libraryScripts.length) return html

  const runtime = await loadThreeRuntime()
  libraryScripts.forEach(script => script.remove())
  // Deduplicate only our exact trusted source, not an untrusted marker alone.
  parsed.querySelectorAll('script[data-xiass-three-runtime]').forEach(script => {
    if (script.textContent === runtime.replace(/<\/script/gi, '<\\/script')) script.remove()
  })
  const script = parsed.createElement('script')
  script.dataset.xiassThreeRuntime = '180'
  script.textContent = runtime.replace(/<\/script/gi, '<\\/script')
  parsed.head.prepend(script)
  return `<!doctype html>${parsed.documentElement.outerHTML}`
}
