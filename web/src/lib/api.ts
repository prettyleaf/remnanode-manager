export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export const UNAUTHORIZED_EVENT = "rnm:unauthorized"

export async function api<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  const method = init?.method ?? "GET"
  const hasBody = method !== "GET"
  const res = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    headers: hasBody ? { "Content-Type": "application/json" } : undefined,
    body: hasBody ? JSON.stringify(init?.body ?? {}) : undefined,
  })

  let data: unknown = null
  const text = await res.text()
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: text }
    }
  }

  if (!res.ok) {
    if (res.status === 401 && !path.startsWith("/auth/")) {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    }
    const body = data as { error?: unknown } | null
    const message = body?.error ? String(body.error) : res.statusText
    throw new ApiError(res.status, message)
  }
  return data as T
}

export const post = <T>(path: string, body?: unknown) => api<T>(path, { method: "POST", body })
export const put = <T>(path: string, body?: unknown) => api<T>(path, { method: "PUT", body })
