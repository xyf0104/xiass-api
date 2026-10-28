export function formatProxyDisplayName(name?: string | null): string {
  const value = name?.trim() ?? ''
  // Keep subscription ownership in stored names; shorten only UI labels.
  return value.match(/^\[Subscription\]\s+.+?\s+\/\s+(.+)$/s)?.[1]?.trim() || value
}
