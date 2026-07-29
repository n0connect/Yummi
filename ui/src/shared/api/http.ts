import { apiURL } from './runtime'

export async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiURL(url), init)
  if (!response.ok) {
    const text = await response.text()
    throw new Error(`HTTP ${response.status}: ${text || response.statusText}`)
  }
  return (await response.json()) as T
}
