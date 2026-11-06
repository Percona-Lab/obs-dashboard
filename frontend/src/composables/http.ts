// errorText returns a failed response's text body, or "HTTP <status>".
export async function errorText(res: Response): Promise<string> {
  const text = (await res.text().catch(() => '')).trim()
  return text || `HTTP ${res.status}`
}
