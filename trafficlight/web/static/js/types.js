// @ts-check
// The shapes that travel between the server and the browser, in one place so every module
// can refer to them. JSDoc only: nothing here exists at runtime.

/**
 * The wire shapes the server sends (see web/serialize.py) plus what the browser adds to them.
 * @typedef {{name: string|null, state: "ok"|"empty"|"unknown"|"error", size: number, text?: string}} MessageSummary
 * @typedef {{method: string|null, value: number, status: string|null, request: MessageSummary,
 *   response: MessageSummary, proxy: ProtoSummary|null}} ProtoSummary
 * @typedef {{id: number, time: number, rpc_id: number, rpc_status: number, rpc_handle: number|null,
 *   protos: ProtoSummary[], rows: Row[], delta: number|null}} LogRecord
 * @typedef {{text: string, tone: string}} Badge
 * @typedef {{key: string, seq: number, record: LogRecord, index: number, proto: ProtoSummary,
 *   method: string, value: number, via: string|null, badge: Badge|null, time: string,
 *   preview: string[], methodText: string, messageText: string, statusText: string,
 *   searchText: string, html: string|null, dropped?: boolean}} Row
 * @typedef {{query: string, caseSensitive: boolean, regex: boolean, matchesOnly: boolean, index: number}} Find
 * @typedef {{id: number, key: string, row: Row, detail: object|null, error: string|null,
 *   request: AbortController|null, sectionState: Map<string, boolean>, renderedDetail: object|null,
 *   scrollTop: number, ephemeral: boolean, stale?: boolean, view: string, find: Find,
 *   el: HTMLElement|null}} Tab
 * @typedef {{start: number, negate: boolean, key: string|null, value: string, regex: string|null,
 *   valueStart: number, end: number}} Token
 */

export {};
