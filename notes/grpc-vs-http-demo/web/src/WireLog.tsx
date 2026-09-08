import { useEffect, useState } from 'react'

import { clearWireLog, subscribe, type WireEntry } from './wire'

export function WireLog() {
  const [entries, setEntries] = useState<WireEntry[]>([])

  useEffect(() => subscribe(setEntries) as unknown as () => void, [])

  return (
    <section className="panel wire">
      <div className="wire-head">
        <h2>Wire log</h2>
        <button type="button" className="ghost" onClick={clearWireLog}>
          Xoá log
        </button>
      </div>
      <p className="note">
        Đây là byte thật đi qua dây. Chú ý cột kích thước và phần body: JSON thì đọc được, protobuf thì
        chỉ còn hex.
      </p>

      <div className="entries">
        {entries.map((entry) => (
          <article key={entry.seq} className={`entry ${entry.transport === 'REST' ? 'rest' : 'grpc'}`}>
            <div className="entry-head">
              <span className="badge">{entry.transport}</span>
              <span className="op">{entry.operation}</span>
              <span className={`status ${entry.failed ? 'bad' : 'good'}`}>{entry.status}</span>
              <span className="ms">{entry.durationMs}ms</span>
            </div>

            <div className="line">
              <span className="method">{entry.method}</span>
              <span className="url">{entry.url}</span>
            </div>

            <Body
              title="Request"
              contentType={entry.requestContentType}
              bytes={entry.requestBytes}
              preview={entry.requestPreview}
            />
            <Body
              title="Response"
              contentType={entry.responseContentType}
              bytes={entry.responseBytes}
              preview={entry.responsePreview}
              tag={entry.responseBytes > 0 ? (entry.humanReadable ? 'đọc được' : 'nhị phân') : undefined}
            />
          </article>
        ))}
        {entries.length === 0 && <p className="empty">Chưa có request nào. Thao tác gì đó ở bên trái.</p>}
      </div>
    </section>
  )
}

function Body({
  title,
  contentType,
  bytes,
  preview,
  tag,
}: {
  title: string
  contentType: string
  bytes: number
  preview: string
  tag?: string
}) {
  return (
    <div className="body">
      <div className="body-head">
        <span>{title}</span>
        <code>{contentType}</code>
        <span className="size">{bytes} B</span>
        {tag && <span className={`tag ${tag === 'nhị phân' ? 'binary' : ''}`}>{tag}</span>}
      </div>
      <pre>{preview}</pre>
    </div>
  )
}
