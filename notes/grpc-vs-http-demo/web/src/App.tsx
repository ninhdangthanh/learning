import { useCallback, useEffect, useMemo, useState } from 'react'

import { grpcApi, GRPC_NATIVE_URL, GRPC_WEB_BASE_URL } from './api/grpc'
import { restApi, REST_BASE_URL } from './api/rest'
import type { Product, ProductDraft, TransportName } from './types'
import { WireLog } from './WireLog'
import { instrumentedFetch, tagNextCall } from './wire'
import './App.css'

const emptyDraft: ProductDraft = { name: '', price: 0, qty: 0 }

const transportInfo: Record<TransportName, { url: string; note: string }> = {
  REST: {
    url: REST_BASE_URL,
    note: 'Browser gọi thẳng. Mỗi thao tác là một URL + verb, body là JSON đọc được bằng mắt.',
  },
  gRPC: {
    url: GRPC_WEB_BASE_URL,
    note: `Browser KHÔNG gọi được gRPC ở ${GRPC_NATIVE_URL}, phải đi vòng qua cầu gRPC-Web. Body là protobuf nhị phân.`,
  },
}

export default function App() {
  const [transport, setTransport] = useState<TransportName>('REST')
  const [products, setProducts] = useState<Product[]>([])
  const [draft, setDraft] = useState<ProductDraft>(emptyDraft)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const api = useMemo(() => (transport === 'REST' ? restApi : grpcApi), [transport])

  const run = useCallback(async (action: () => Promise<void>) => {
    setBusy(true)
    setError(null)
    try {
      await action()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }, [])

  const refresh = useCallback(async () => {
    setProducts(await api.list())
  }, [api])

  useEffect(() => {
    run(refresh)
  }, [run, refresh])

  const resetForm = () => {
    setDraft(emptyDraft)
    setEditingId(null)
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    run(async () => {
      if (editingId) {
        await api.update(editingId, draft)
      } else {
        await api.create(draft)
      }
      resetForm()
      await refresh()
    })
  }

  const startEdit = (product: Product) => {
    setEditingId(product.id)
    setDraft({ name: product.name, price: product.price, qty: product.qty })
  }

  const remove = (id: string) => {
    run(async () => {
      await api.remove(id)
      if (editingId === id) resetForm()
      await refresh()
    })
  }

  const probeNativeGrpc = () => {
    run(async () => {
      tagNextCall('gRPC', 'Gọi thẳng cổng gRPC thuần')
      try {
        await instrumentedFetch(GRPC_NATIVE_URL, { method: 'POST' })
        setError('Bất ngờ: request không lỗi — xem chi tiết trong Wire log.')
      } catch {
        setError(
          `Đúng như dự đoán: browser không nói được gRPC thuần ở ${GRPC_NATIVE_URL}. ` +
            'fetch() chỉ biết HTTP/1.1 và HTTP/2 kiểu request-response, còn gRPC cần điều khiển frame HTTP/2 và đọc trailer.',
        )
      }
    })
  }

  return (
    <div className="page">
      <header className="hero">
        <h1>CRUD một store, hai đường vào</h1>
        <p>
          Cùng một <code>map</code> in-memory trong Go. Đổi transport ở dưới rồi nhìn Wire log để thấy
          HTTP và gRPC khác nhau chỗ nào trên dây.
        </p>
      </header>

      <div className="ports">
        <Port label="REST/JSON" addr=":8080" who="browser, curl, Postman" tone="ok" />
        <Port label="gRPC thuần" addr=":50051" who="Go client, grpcurl — KHÔNG có browser" tone="warn" />
        <Port label="gRPC-Web bridge" addr=":8082" who="browser, sau khi dịch" tone="bridge" />
      </div>

      <div className="layout">
        <section className="panel">
          <div className="switcher">
            {(['REST', 'gRPC'] as TransportName[]).map((name) => (
              <button
                key={name}
                type="button"
                className={name === transport ? 'active' : ''}
                onClick={() => setTransport(name)}
              >
                {name}
              </button>
            ))}
            <span className="switcher-url">{transportInfo[transport].url}</span>
          </div>
          <p className="note">{transportInfo[transport].note}</p>

          <form className="form" onSubmit={submit}>
            <input
              placeholder="Tên sản phẩm"
              value={draft.name}
              onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              required
            />
            <input
              type="number"
              placeholder="Giá"
              value={draft.price}
              onChange={(e) => setDraft({ ...draft, price: Number(e.target.value) })}
            />
            <input
              type="number"
              placeholder="SL"
              value={draft.qty}
              onChange={(e) => setDraft({ ...draft, qty: Number(e.target.value) })}
            />
            <button type="submit" disabled={busy}>
              {editingId ? 'Cập nhật' : 'Thêm'}
            </button>
            {editingId && (
              <button type="button" className="ghost" onClick={resetForm}>
                Huỷ
              </button>
            )}
          </form>

          {error && <p className="error">{error}</p>}

          <table className="table">
            <thead>
              <tr>
                <th>ID</th>
                <th>Tên</th>
                <th className="num">Giá</th>
                <th className="num">SL</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {products.map((product) => (
                <tr key={product.id} className={product.id === editingId ? 'editing' : ''}>
                  <td className="mono">{product.id}</td>
                  <td>{product.name}</td>
                  <td className="num">{product.price.toLocaleString('vi-VN')}</td>
                  <td className="num">{product.qty}</td>
                  <td className="actions">
                    <button type="button" className="ghost" onClick={() => startEdit(product)}>
                      Sửa
                    </button>
                    <button type="button" className="ghost danger" onClick={() => remove(product.id)}>
                      Xoá
                    </button>
                  </td>
                </tr>
              ))}
              {products.length === 0 && (
                <tr>
                  <td colSpan={5} className="empty">
                    Chưa có sản phẩm nào.
                  </td>
                </tr>
              )}
            </tbody>
          </table>

          <button type="button" className="probe" onClick={probeNativeGrpc} disabled={busy}>
            Thử gọi thẳng cổng gRPC thuần {GRPC_NATIVE_URL}
          </button>
        </section>

        <WireLog />
      </div>
    </div>
  )
}

function Port({
  label,
  addr,
  who,
  tone,
}: {
  label: string
  addr: string
  who: string
  tone: 'ok' | 'warn' | 'bridge'
}) {
  return (
    <div className={`port ${tone}`}>
      <div className="port-head">
        <strong>{label}</strong>
        <code>{addr}</code>
      </div>
      <span>{who}</span>
    </div>
  )
}
