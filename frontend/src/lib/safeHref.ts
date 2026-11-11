// Returns the URL only when it is an absolute http(s) URL, so server-provided
// links can never become javascript: or data: hrefs.
export function safeHref(url: string): string | undefined {
  return /^https?:\/\//i.test(url) ? url : undefined
}
