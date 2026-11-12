// This document is only assigned to an opaque sandbox="allow-scripts" iframe.
const policy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'; frame-src 'none';"

function sizePreview() {
  addEventListener('load', () => {
    const root = document.documentElement
    let scheduled = false
    const report = () => {
      scheduled = false
      root.style.setProperty('transform', 'none', 'important')
      root.style.setProperty('transform-origin', '0 0', 'important')
      root.style.setProperty('overflow', 'hidden', 'important')
      const width = Math.max(1100, root.scrollWidth, document.body?.scrollWidth || 0)
      const height = Math.max(690, root.scrollHeight, document.body?.scrollHeight || 0)
      const scale = Math.min(1100 / width, 690 / height)
      root.style.setProperty('transform', `translate(${(1100 - width * scale) / 2}px,${(690 - height * scale) / 2}px) scale(${scale})`, 'important')
    }
    const schedule = () => {
      if (!scheduled) {
        scheduled = true
        requestAnimationFrame(report)
      }
    }
    report()
    const observer = new ResizeObserver(schedule)
    observer.observe(root)
    if (document.body) observer.observe(document.body)
    new MutationObserver(schedule).observe(root, { subtree: true, childList: true, characterData: true })
    root.addEventListener('load', schedule, true)
    document.fonts?.ready.then(schedule)
  })
}

export function buildPelicanPreviewDocument(html: string): string {
  // srcdoc inherits the panel's nonce policy. Authorize inline scripts inside
  // this isolated document, without relaxing the panel or allowing external JS.
  const nonce = document.querySelector<HTMLScriptElement>('script[nonce]')?.nonce || ''
  const preview = new DOMParser().parseFromString(`<meta http-equiv="Content-Security-Policy" content="${policy}">${html}`, 'text/html')
  for (const script of preview.querySelectorAll('script')) {
    script.removeAttribute('nonce')
    if (!['src', 'href', 'xlink:href'].some(name => script.hasAttribute(name)) && nonce) script.setAttribute('nonce', nonce)
  }
  const createScript = (source: string) => {
    const script = preview.createElement('script')
    if (nonce) script.setAttribute('nonce', nonce)
    script.textContent = source.replace(/<\/script/gi, '<\\/script')
    return script
  }
  // Inline event attributes cannot carry a nonce. Keep their normal event/this
  // behavior and element/form scope with script-defined handlers. Install them
  // during parsing before the element's children, so later scripts can override.
  const adjacentScriptElements = new Set(['AREA', 'BASE', 'BR', 'COL', 'EMBED', 'HR', 'IMG', 'INPUT', 'LINK', 'META', 'PARAM', 'SOURCE', 'TRACK', 'WBR', 'TEXTAREA', 'TITLE', 'STYLE', 'SCRIPT', 'IFRAME', 'XMP', 'NOEMBED', 'NOFRAMES'])
  const policyElement = preview.head.firstElementChild!
  for (const element of preview.querySelectorAll('[data-xiass-preview-event]')) element.removeAttribute('data-xiass-preview-event')
  let index = 0
  for (const element of preview.querySelectorAll('*')) {
    const handlers = element.getAttributeNames().filter(name => /^on[a-z]+$/.test(name))
    if (!handlers.length) continue
    const selector = `[data-xiass-preview-event="${index++}"]`
    element.setAttribute('data-xiass-preview-event', String(index - 1))
    const assignments = handlers.map(name => {
      const source = element.getAttribute(name) || ''
      element.removeAttribute(name)
      return `element[${JSON.stringify(name)}]=(function(){with(document){with(element.form||{}){with(element){return function(event){\n${source}\n};}}}})();`
    }).join('\n')
    const script = createScript(`(function(element){${assignments}})(document.querySelector(${JSON.stringify(selector)}));`)
    if (element === preview.documentElement || element === preview.head) policyElement.after(script)
    else if (adjacentScriptElements.has(element.tagName)) element.after(script)
    else element.prepend(script)
  }
  preview.body.appendChild(createScript(`(${sizePreview.toString()})();`))
  return `<!doctype html>${preview.documentElement.outerHTML}`
}
