const BASE = ''

export class APIError extends Error {
  constructor(
    public status: number,
    message: string
  ) {
    super(message)
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...init?.headers },
    ...init
  })

  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new APIError(res.status, text || res.statusText)
  }

  if (res.status === 204) {
    return undefined as T
  }

  return res.json() as Promise<T>
}

export async function apiRequest(path: string, init?: RequestInit): Promise<Response> {
  return fetch(`${BASE}${path}`, init)
}

export async function getHealth(): Promise<{ status: string; time: string }> {
  return apiFetch('/health')
}
