export interface Product {
  id: string
  name: string
  price: number
  qty: number
}

export interface ProductDraft {
  name: string
  price: number
  qty: number
}

export interface ProductApi {
  list(): Promise<Product[]>
  create(draft: ProductDraft): Promise<Product>
  update(id: string, draft: ProductDraft): Promise<Product>
  remove(id: string): Promise<void>
}

export type TransportName = 'REST' | 'gRPC'
