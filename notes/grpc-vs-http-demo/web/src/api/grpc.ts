import { createGrpcWebTransport } from '@connectrpc/connect-web'
import { createPromiseClient } from '@connectrpc/connect'

import { ProductService } from '../gen/product/v1/product_connect'
import type { Product as ProtoProduct } from '../gen/product/v1/product_pb'
import type { Product, ProductApi, ProductDraft } from '../types'
import { instrumentedFetch, tagNextCall } from '../wire'

export const GRPC_WEB_BASE_URL = 'http://localhost:8082'
export const GRPC_NATIVE_URL = 'http://localhost:50051'

const transport = createGrpcWebTransport({
  baseUrl: GRPC_WEB_BASE_URL,
  fetch: instrumentedFetch,
})

const client = createPromiseClient(ProductService, transport)

function fromProto(p: ProtoProduct | undefined): Product {
  if (!p) throw new Error('server trả về product rỗng')
  return { id: p.id, name: p.name, price: Number(p.price), qty: p.qty }
}

export const grpcApi: ProductApi = {
  async list() {
    tagNextCall('gRPC', 'ListProducts')
    const response = await client.listProducts({})
    return response.products.map(fromProto)
  },

  async create(draft: ProductDraft) {
    tagNextCall('gRPC', 'CreateProduct')
    const response = await client.createProduct({
      name: draft.name,
      price: BigInt(draft.price),
      qty: draft.qty,
    })
    return fromProto(response.product)
  },

  async update(id: string, draft: ProductDraft) {
    tagNextCall('gRPC', 'UpdateProduct')
    const response = await client.updateProduct({
      id,
      name: draft.name,
      price: BigInt(draft.price),
      qty: draft.qty,
    })
    return fromProto(response.product)
  },

  async remove(id: string) {
    tagNextCall('gRPC', 'DeleteProduct')
    await client.deleteProduct({ id })
  },
}
