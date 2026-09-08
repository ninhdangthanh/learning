import type { Product, ProductApi, ProductDraft } from '../types'
import { instrumentedFetch, tagNextCall } from '../wire'

export const REST_BASE_URL = 'http://localhost:8080'

async function call<T>(operation: string, path: string, init?: RequestInit): Promise<T> {
  tagNextCall('REST', operation)
  const response = await instrumentedFetch(`${REST_BASE_URL}${path}`, init)

  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }))
    throw new Error(`HTTP ${response.status}: ${body.error ?? response.statusText}`)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

function jsonBody(draft: ProductDraft): RequestInit {
  return {
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(draft),
  }
}

export const restApi: ProductApi = {
  list: () => call<Product[]>('ListProducts', '/api/products'),

  create: (draft) =>
    call<Product>('CreateProduct', '/api/products', { method: 'POST', ...jsonBody(draft) }),

  update: (id, draft) =>
    call<Product>('UpdateProduct', `/api/products/${id}`, { method: 'PUT', ...jsonBody(draft) }),

  remove: (id) => call<void>('DeleteProduct', `/api/products/${id}`, { method: 'DELETE' }),
}
