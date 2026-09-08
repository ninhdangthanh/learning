import type { TransportName } from './types'

export interface WireEntry {
  seq: number
  transport: TransportName
  operation: string
  method: string
  url: string
  status: string
  durationMs: number
  requestContentType: string
  requestBytes: number
  requestPreview: string
  responseContentType: string
  responseBytes: number
  responsePreview: string
  humanReadable: boolean
  failed: boolean
}

type Listener = (entries: WireEntry[]) => void

let seq = 0
let entries: WireEntry[] = []
const listeners = new Set<Listener>()

let currentTransport: TransportName = 'REST'
let currentOperation = '-'

export function tagNextCall(transport: TransportName, operation: string) {
  currentTransport = transport
  currentOperation = operation
}

export function subscribe(listener: Listener) {
  listeners.add(listener)
  listener(entries)
  return () => listeners.delete(listener)
}

export function clearWireLog() {
  entries = []
  emit()
}

function emit() {
  for (const listener of listeners) listener(entries)
}

function push(entry: Omit<WireEntry, 'seq'>) {
  entries = [{ seq: ++seq, ...entry }, ...entries].slice(0, 40)
  emit()
}

export function pushManualEntry(entry: Omit<WireEntry, 'seq'>) {
  push(entry)
}

const textDecoder = new TextDecoder('utf-8', { fatal: false })

function isMostlyPrintable(bytes: Uint8Array) {
  if (bytes.length === 0) return true
  let printable = 0
  for (const byte of bytes) {
    if (byte === 9 || byte === 10 || byte === 13 || (byte >= 32 && byte !== 127)) printable++
  }
  return printable / bytes.length > 0.9
}

function preview(bytes: Uint8Array) {
  if (bytes.length === 0) return '(rỗng)'
  if (isMostlyPrintable(bytes)) return textDecoder.decode(bytes.slice(0, 400))
  const head = Array.from(bytes.slice(0, 40), (b) => b.toString(16).padStart(2, '0')).join(' ')
  return bytes.length > 40 ? `${head} …` : head
}

async function toBytes(body: unknown): Promise<Uint8Array> {
  if (body == null) return new Uint8Array()
  if (typeof body === 'string') return new TextEncoder().encode(body)
  if (body instanceof Uint8Array) return body
  if (body instanceof ArrayBuffer) return new Uint8Array(body)
  if (body instanceof Blob) return new Uint8Array(await body.arrayBuffer())
  return new TextEncoder().encode(String(body))
}

function headerValue(init: RequestInit | undefined, request: Request | undefined, name: string) {
  if (init?.headers) {
    const headers = new Headers(init.headers as HeadersInit)
    const found = headers.get(name)
    if (found) return found
  }
  return request?.headers.get(name) ?? '-'
}

export const instrumentedFetch: typeof fetch = async (input, init) => {
  const request = input instanceof Request ? input : undefined
  const url = request ? request.url : String(input)
  const method = init?.method ?? request?.method ?? 'GET'
  const transport = currentTransport
  const operation = currentOperation

  const requestBody = init?.body ?? (request ? await request.clone().arrayBuffer() : null)
  const requestBytes = await toBytes(requestBody)
  const startedAt = performance.now()

  try {
    const response = await fetch(input as RequestInfo, init)
    const responseBytes = new Uint8Array(await response.clone().arrayBuffer())

    push({
      transport,
      operation,
      method,
      url,
      status: `${response.status} ${response.statusText}`.trim(),
      durationMs: Math.round(performance.now() - startedAt),
      requestContentType: headerValue(init, request, 'content-type'),
      requestBytes: requestBytes.length,
      requestPreview: preview(requestBytes),
      responseContentType: response.headers.get('content-type') ?? '-',
      responseBytes: responseBytes.length,
      responsePreview: preview(responseBytes),
      humanReadable: isMostlyPrintable(responseBytes),
      failed: !response.ok,
    })

    return response
  } catch (error) {
    push({
      transport,
      operation,
      method,
      url,
      status: 'NETWORK ERROR',
      durationMs: Math.round(performance.now() - startedAt),
      requestContentType: headerValue(init, request, 'content-type'),
      requestBytes: requestBytes.length,
      requestPreview: preview(requestBytes),
      responseContentType: '-',
      responseBytes: 0,
      responsePreview: String(error),
      humanReadable: true,
      failed: true,
    })
    throw error
  }
}
