'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { Toaster, toast } from 'react-hot-toast'
import { AnimatePresence, motion } from 'framer-motion'
import {
  Plus,
  Trash2,
  Save,
  Loader2,
  Send,
  Timer,
  AlertTriangle,
  CircleCheck,
  Boxes,
  ChevronDown,
  ChevronRight,
  FolderClosed,
  X,
  Copy,
  Check,
  Waypoints,
  KeyRound,
  Lock,
  Unlock,
  Star,
  Search,
  Settings,
  Radio,
  ArrowDownToLine,
  ArrowUpFromLine,
  Repeat,
  Zap,
  FlaskConical,
  Cable,
} from 'lucide-react'
import Editor, { type Monaco } from '@monaco-editor/react'

interface ProtoMethod {
  name: string
  service: string
  type: 'unary' | 'server_stream' | 'client_stream' | 'bidi_stream'
  requestType: string
  responseType: string
}

interface SavedRequest {
  id: string
  name: string
  method: string
  service: string
  request: string
  metadata: Record<string, string>
  favorite?: boolean
}

interface Tab {
  id: string
  name: string
  method: string
  service: string
  request: string
  response: string
  metadata: Record<string, string>
  streamEvents: string[]
  status: 'success' | 'error' | null
  responseTime: number | null
  isLoading: boolean
  isStreaming: boolean
}

const DEFAULT_REQUEST: Tab = {
  id: '1',
  name: 'New request',
  method: '',
  service: '',
  request: '{}',
  response: '',
  metadata: {},
  streamEvents: [],
  status: null,
  responseTime: null,
  isLoading: false,
  isStreaming: false,
}

const DEFAULT_REQUESTS: SavedRequest[] = [
  {
    id: '1',
    name: 'Create user',
    method: '/example.v1.UserService/CreateUser',
    service: 'UserService',
    request: JSON.stringify({ name: 'Ada Lovelace', email: 'ada@example.com', age: 36 }, null, 2),
    metadata: {},
    favorite: true,
  },
  {
    id: '2',
    name: 'Get user',
    method: '/example.v1.UserService/GetUser',
    service: 'UserService',
    request: JSON.stringify({ id: 'user-42' }, null, 2),
    metadata: {},
  },
  {
    id: '3',
    name: 'Stream events',
    method: '/example.v1.UserService/StreamUserEvents',
    service: 'UserService',
    request: JSON.stringify({ user_id: 'user-42', event_types: ['login', 'purchase'] }, null, 2),
    metadata: {},
  },
]

const services: { [key: string]: ProtoMethod[] } = {
  UserService: [
    { name: '/example.v1.UserService/GetUser', service: 'UserService', type: 'unary', requestType: 'GetUserRequest', responseType: 'User' },
    { name: '/example.v1.UserService/CreateUser', service: 'UserService', type: 'unary', requestType: 'CreateUserRequest', responseType: 'User' },
    { name: '/example.v1.UserService/UpdateUser', service: 'UserService', type: 'unary', requestType: 'UpdateUserRequest', responseType: 'User' },
    { name: '/example.v1.UserService/DeleteUser', service: 'UserService', type: 'unary', requestType: 'DeleteUserRequest', responseType: 'Empty' },
    { name: '/example.v1.UserService/ListUsers', service: 'UserService', type: 'unary', requestType: 'ListUsersRequest', responseType: 'ListUsersResponse' },
    { name: '/example.v1.UserService/StreamUserEvents', service: 'UserService', type: 'server_stream', requestType: 'StreamUserEventsRequest', responseType: 'stream UserEvent' },
    { name: '/example.v1.UserService/CollectUserMetrics', service: 'UserService', type: 'client_stream', requestType: 'stream UserMetric', responseType: 'CollectMetricsResponse' },
    { name: '/example.v1.UserService/ChatStream', service: 'UserService', type: 'bidi_stream', requestType: 'stream ChatMessage', responseType: 'stream ChatMessage' },
  ],
}

type MethodType = ProtoMethod['type']

const methodMeta: Record<MethodType, { label: string; color: string; Icon: typeof Zap }> = {
  unary: { label: 'UNARY', color: 'var(--emerald)', Icon: Zap },
  server_stream: { label: 'SERVER', color: 'var(--cyan)', Icon: ArrowDownToLine },
  client_stream: { label: 'CLIENT', color: 'var(--amber)', Icon: ArrowUpFromLine },
  bidi_stream: { label: 'BIDI', color: 'var(--rose)', Icon: Repeat },
}

function methodTypeOf(name: string): MethodType {
  return Object.values(services).flat().find((m) => m.name === name)?.type ?? 'unary'
}

function TypeBadge({ type, dense = false }: { type: MethodType; dense?: boolean }) {
  const m = methodMeta[type]
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-md border border-line-2/60 bg-ink-760 px-1.5 py-[3px] font-mono text-[10px] font-medium tracking-[0.12em] text-txt-2"
      style={{ borderColor: dense ? undefined : undefined }}
    >
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: m.color, boxShadow: `0 0 6px ${m.color}` }} />
      {m.label}
    </span>
  )
}

function defineSignalTheme(monaco: Monaco) {
  monaco.editor.defineTheme('signal-ink', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'string.key.json', foreground: '98a0ac' },
      { token: 'string.value.json', foreground: 'c7f04a' },
      { token: 'number', foreground: '63d3e8' },
      { token: 'keyword.json', foreground: 'f2748d' },
      { token: 'delimiter', foreground: '5a616d' },
      { token: '', foreground: 'e8eaf0' },
    ],
    colors: {
      'editor.background': '#00000000',
      'editor.foreground': '#e8eaf0',
      'editorLineNumber.foreground': '#3a4150',
      'editorLineNumber.activeForeground': '#98a0ac',
      'editor.selectionBackground': '#c7f04a2e',
      'editor.lineHighlightBackground': '#ffffff07',
      'editor.lineHighlightBorder': '#00000000',
      'editorCursor.foreground': '#c7f04a',
      'editorIndentGuide.background1': '#1d212a',
      'editorWidget.background': '#121419',
      'editorWidget.border': '#242832',
    },
  })
}

export default function Home() {
  const [serverAddress, setServerAddress] = useState('127.0.0.1:50051')
  const [useTLS, setUseTLS] = useState(false)
  const [tabs, setTabs] = useState<Tab[]>([DEFAULT_REQUEST])
  const [activeTab, setActiveTab] = useState('1')
  const [savedRequests, setSavedRequests] = useState<SavedRequest[]>(DEFAULT_REQUESTS)
  const [expandedServices, setExpandedServices] = useState<Set<string>>(new Set(['UserService']))
  const [showSaveModal, setShowSaveModal] = useState(false)
  const [requestName, setRequestName] = useState('')
  const [searchQuery, setSearchQuery] = useState('')
  const [showMetadata, setShowMetadata] = useState(false)
  const [copied, setCopied] = useState(false)
  const nextTabID = useRef(2)
  const copyResetTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const abortControllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    return () => {
      if (copyResetTimeoutRef.current !== null) clearTimeout(copyResetTimeoutRef.current)
      abortControllerRef.current?.abort()
    }
  }, [])

  const activeTabData = tabs.find((t) => t.id === activeTab) || tabs[0]

  const updateTab = useCallback((id: string, updates: Partial<Tab>) => {
    setTabs((current) => current.map((t) => (t.id === id ? { ...t, ...updates } : t)))
  }, [])

  const addNewTab = useCallback(() => {
    const newTab: Tab = { ...DEFAULT_REQUEST, id: `tab-${nextTabID.current++}`, name: 'New request' }
    setTabs((current) => [...current, newTab])
    setActiveTab(newTab.id)
  }, [])

  const closeTab = (id: string, e?: React.MouseEvent) => {
    e?.stopPropagation()
    if (tabs.length === 1) return
    const index = tabs.findIndex((t) => t.id === id)
    const newTabs = tabs.filter((t) => t.id !== id)
    setTabs(newTabs)
    if (activeTab === id) setActiveTab(newTabs[Math.max(0, index - 1)].id)
  }

  const loadRequest = (req: SavedRequest, asNewTab = false) => {
    const tabData: Tab = {
      ...DEFAULT_REQUEST,
      id: asNewTab ? `tab-${nextTabID.current++}` : activeTab,
      name: req.name,
      method: req.method,
      service: req.service,
      request: req.request,
      metadata: req.metadata,
    }
    if (asNewTab) {
      setTabs((current) => [...current, tabData])
      setActiveTab(tabData.id)
    } else {
      updateTab(activeTab, tabData)
    }
  }

  const handleSend = useCallback(async () => {
    if (!activeTabData.method) {
      toast.error('Select a method first')
      return
    }
    abortControllerRef.current?.abort()
    const controller = new AbortController()
    abortControllerRef.current = controller
    const tabIdAtStart = activeTab

    updateTab(tabIdAtStart, { isLoading: true, status: null, response: '', streamEvents: [], responseTime: null })

    const startTime = Date.now()
    const sleep = (ms: number) =>
      new Promise<void>((resolve, reject) => {
        const timer = setTimeout(resolve, ms)
        controller.signal.addEventListener('abort', () => {
          clearTimeout(timer)
          reject(new DOMException('Request aborted', 'AbortError'))
        })
      })

    try {
      const reqBody = JSON.parse(activeTabData.request || '{}') as Record<string, unknown>
      await sleep(300)
      let mockResponse: Record<string, unknown> = {}
      const now = new Date().toISOString()

      switch (activeTabData.method) {
        case '/example.v1.UserService/GetUser':
          if (!reqBody.id) throw new Error('id is required')
          mockResponse = { id: reqBody.id, name: 'Ada Lovelace', email: 'ada@example.com', age: 36, created_at: now, updated_at: now }
          break
        case '/example.v1.UserService/CreateUser':
          if (!reqBody.name) throw new Error('name is required')
          if (!reqBody.email) throw new Error('email is required')
          mockResponse = { id: 'user-' + Date.now(), name: reqBody.name, email: reqBody.email, age: reqBody.age || 0, created_at: now, updated_at: now }
          break
        case '/example.v1.UserService/UpdateUser':
          if (!reqBody.id) throw new Error('id is required')
          mockResponse = { id: reqBody.id, name: reqBody.name || 'Updated Name', email: reqBody.email || 'updated@example.com', age: reqBody.age || 25, created_at: now, updated_at: now }
          break
        case '/example.v1.UserService/DeleteUser':
          if (!reqBody.id) throw new Error('id is required')
          mockResponse = { success: true, message: 'User deleted successfully' }
          break
        case '/example.v1.UserService/ListUsers':
          mockResponse = {
            users: [
              { id: 'user-41', name: 'Grace Hopper', email: 'grace@example.com', age: 41 },
              { id: 'user-42', name: 'Ada Lovelace', email: 'ada@example.com', age: 36 },
            ],
            next_page_token: 'dXNlci00Mg',
          }
          break
        case '/example.v1.UserService/StreamUserEvents': {
          if (!reqBody.user_id) throw new Error('user_id is required')
          updateTab(tabIdAtStart, { isStreaming: true })
          const events = ['login', 'logout', 'purchase', 'click', 'view']
          for (let i = 0; i < 5; i++) {
            await sleep(220)
            const event = JSON.stringify(
              { user_id: reqBody.user_id, event_type: events[i % events.length], payload: JSON.stringify({ data: i, action: events[i % events.length] }), timestamp: new Date().toISOString() },
              null,
              2,
            )
            setTabs((current) => current.map((tab) => (tab.id === tabIdAtStart ? { ...tab, streamEvents: [...tab.streamEvents, event] } : tab)))
          }
          updateTab(tabIdAtStart, { isStreaming: false })
          mockResponse = { message: 'Stream completed', events_count: 5 }
          break
        }
        case '/example.v1.UserService/CollectUserMetrics':
          mockResponse = { count: 10, sum: 500, avg: 50 }
          break
        case '/example.v1.UserService/ChatStream':
          mockResponse = { id: 'msg-' + Date.now(), user_id: reqBody.user_id || 'user-1', message: reqBody.message || 'Hello from server', timestamp: now }
          break
        default:
          mockResponse = { message: 'Method not fully implemented in demo mode' }
      }

      updateTab(tabIdAtStart, { response: JSON.stringify(mockResponse, null, 2), responseTime: Date.now() - startTime, status: 'success', isLoading: false })
      toast.success('Response received')
    } catch (err: unknown) {
      if (controller.signal.aborted) return
      const message = err instanceof Error ? err.message : 'Request failed'
      updateTab(tabIdAtStart, { response: JSON.stringify({ error: message }, null, 2), responseTime: Date.now() - startTime, status: 'error', isLoading: false })
      toast.error(message)
    }
  }, [activeTab, activeTabData, updateTab])

  const handleSave = () => {
    if (!requestName.trim()) {
      toast.error('Enter a name')
      return
    }
    const newRequest: SavedRequest = {
      id: Date.now().toString(),
      name: requestName,
      method: activeTabData.method,
      service: activeTabData.service,
      request: activeTabData.request,
      metadata: activeTabData.metadata,
    }
    setSavedRequests((current) => [...current, newRequest])
    setShowSaveModal(false)
    setRequestName('')
    toast.success('Request saved')
  }

  const toggleFavorite = (id: string) => {
    setSavedRequests((current) => current.map((r) => (r.id === id ? { ...r, favorite: !r.favorite } : r)))
  }

  const copyResponse = () => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) {
      toast.error('Clipboard not available')
      return
    }
    navigator.clipboard.writeText(activeTabData.response).catch(() => toast.error('Failed to copy'))
    setCopied(true)
    if (copyResetTimeoutRef.current !== null) clearTimeout(copyResetTimeoutRef.current)
    copyResetTimeoutRef.current = setTimeout(() => setCopied(false), 2000)
  }

  const toggleService = (service: string) => {
    const next = new Set(expandedServices)
    next.has(service) ? next.delete(service) : next.add(service)
    setExpandedServices(next)
  }

  const filteredRequests = savedRequests.filter(
    (r) => r.name.toLowerCase().includes(searchQuery.toLowerCase()) || r.method.toLowerCase().includes(searchQuery.toLowerCase()),
  )
  const favoriteRequests = filteredRequests.filter((r) => r.favorite)
  const otherRequests = filteredRequests.filter((r) => !r.favorite)

  const editorOptions = {
    minimap: { enabled: false },
    fontSize: 13,
    lineNumbers: 'on' as const,
    scrollBeyondLastLine: false,
    automaticLayout: true,
    tabSize: 2,
    fontFamily: 'var(--font-mono), monospace',
    fontLigatures: true,
    padding: { top: 14 },
    renderLineHighlight: 'all' as const,
    scrollbar: { verticalScrollbarSize: 9, horizontalScrollbarSize: 9 },
    overviewRulerLanes: 0,
  }

  return (
    <div className="relative flex h-screen flex-col overflow-hidden bg-ink-900 text-txt">
      <div className="app-backdrop" />
      <div className="app-grid" />
      <div className="app-grain" />

      <Toaster
        position="top-right"
        toastOptions={{
          style: {
            background: 'var(--ink-760)',
            color: 'var(--txt)',
            border: '1px solid var(--line-2)',
            fontSize: '13px',
            borderRadius: '10px',
            boxShadow: '0 20px 50px -18px rgba(0,0,0,0.7)',
          },
          success: { iconTheme: { primary: 'var(--signal)', secondary: 'var(--ink-900)' } },
          error: { iconTheme: { primary: 'var(--rose)', secondary: 'var(--ink-900)' } },
        }}
      />

      {/* ── Header ─────────────────────────────────────────────────────── */}
      <header className="relative z-10 flex h-14 shrink-0 items-center gap-4 border-b border-line bg-ink-800/80 px-4 backdrop-blur">
        <div className="flex items-center gap-2.5">
          <div className="grid h-8 w-8 place-items-center rounded-lg border border-signal-line bg-signal-soft">
            <Waypoints className="h-4 w-4 text-signal" strokeWidth={2.25} />
          </div>
          <div className="leading-none">
            <div className="font-mono text-[15px] font-semibold tracking-tight text-txt">
              signal<span className="text-signal">.</span>
            </div>
            <div className="mt-0.5 font-mono text-[10px] uppercase tracking-[0.22em] text-txt-3">gRPC console</div>
          </div>
        </div>

        <div className="mx-2 hidden h-6 w-px bg-line md:block" />

        <div className="mx-auto w-full max-w-lg md:mx-0">
          <div className="group relative flex items-center rounded-lg border border-line bg-ink-900/70 focus-within:border-signal-line">
            <Radio className="ml-3 h-4 w-4 text-txt-3" />
            <input
              value={serverAddress}
              onChange={(e) => setServerAddress(e.target.value)}
              spellCheck={false}
              className="w-full bg-transparent px-3 py-1.5 font-mono text-sm text-txt placeholder-txt-3 focus:outline-none"
              placeholder="host:port"
            />
            <span className="mr-3 flex items-center gap-1.5">
              <span className="signal-dot" />
              <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-txt-3">live</span>
            </span>
          </div>
        </div>

        <div className="ml-auto flex items-center gap-2">
          <Segmented active={useTLS} onClick={() => setUseTLS(!useTLS)} title="TLS">
            {useTLS ? <Lock className="h-3.5 w-3.5" /> : <Unlock className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">TLS</span>
          </Segmented>
          <Segmented active={showMetadata} onClick={() => setShowMetadata(!showMetadata)} title="Metadata">
            <KeyRound className="h-3.5 w-3.5" />
            <span className="hidden sm:inline">Metadata</span>
          </Segmented>
          <button className="grid h-8 w-8 place-items-center rounded-lg border border-line text-txt-2 transition-colors hover:border-line-2 hover:text-txt">
            <Settings className="h-4 w-4" />
          </button>
          <span className="hidden items-center gap-1.5 rounded-lg border border-line bg-ink-760 px-2.5 py-1.5 font-mono text-[11px] text-txt-2 xl:inline-flex">
            <FlaskConical className="h-3.5 w-3.5 text-amber-signal" />
            demo · simulated locally
          </span>
        </div>
      </header>

      {/* ── Body ───────────────────────────────────────────────────────── */}
      <div className="relative z-10 flex min-h-0 flex-1">
        {/* Sidebar */}
        <aside className="flex w-72 shrink-0 flex-col border-r border-line bg-ink-850/70">
          <div className="space-y-3 border-b border-line p-3">
            <div className="relative flex items-center rounded-lg border border-line bg-ink-900/70 focus-within:border-signal-line">
              <Search className="ml-2.5 h-3.5 w-3.5 text-txt-3" />
              <input
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full bg-transparent px-2.5 py-1.5 text-xs text-txt placeholder-txt-3 focus:outline-none"
                placeholder="Filter requests"
              />
            </div>
            <button
              onClick={addNewTab}
              className="flex w-full items-center justify-center gap-2 rounded-lg border border-signal-line bg-signal-soft py-1.5 text-sm font-medium text-signal transition-all hover:shadow-signal"
            >
              <Plus className="h-4 w-4" />
              New request
            </button>
          </div>

          <div className="flex-1 overflow-y-auto px-2 py-3">
            {favoriteRequests.length > 0 && (
              <div className="mb-2">
                <SectionLabel icon={<Star className="h-3 w-3" />}>Favorites</SectionLabel>
                {favoriteRequests.map((req) => (
                  <RequestRow key={req.id} req={req} onOpen={() => loadRequest(req)} onFav={() => toggleFavorite(req.id)} favorite />
                ))}
              </div>
            )}

            <SectionLabel icon={<Boxes className="h-3 w-3" />}>Services</SectionLabel>
            {Object.keys(services).map((service) => {
              const open = expandedServices.has(service)
              return (
                <div key={service} className="mb-1">
                  <button
                    onClick={() => toggleService(service)}
                    className="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-ink-760"
                  >
                    {open ? <ChevronDown className="h-3.5 w-3.5 text-txt-3" /> : <ChevronRight className="h-3.5 w-3.5 text-txt-3" />}
                    <FolderClosed className="h-3.5 w-3.5 text-txt-2" />
                    <span className="font-mono text-[13px] text-txt">{service}</span>
                    <span className="ml-auto rounded bg-ink-760 px-1.5 font-mono text-[10px] text-txt-3">{services[service].length}</span>
                  </button>
                  <AnimatePresence initial={false}>
                    {open && (
                      <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.22, ease: [0.2, 0.7, 0.2, 1] }}
                        className="overflow-hidden"
                      >
                        <div className="ml-3 border-l border-line pl-1.5">
                          {services[service].map((method, i) => {
                            const active = activeTabData.method === method.name
                            return (
                              <motion.button
                                key={method.name}
                                initial={{ opacity: 0, x: -6 }}
                                animate={{ opacity: 1, x: 0 }}
                                transition={{ delay: i * 0.03, duration: 0.3 }}
                                onClick={() => updateTab(activeTab, { method: method.name, service: method.service, request: getDefaultRequest(method.type) })}
                                className={`group relative flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors ${
                                  active ? 'bg-ink-760' : 'hover:bg-ink-800'
                                }`}
                              >
                                {active && <span className="absolute -left-[7px] top-1/2 h-4 w-[2px] -translate-y-1/2 rounded-full bg-signal shadow-[0_0_8px_var(--signal)]" />}
                                <TypeBadge type={method.type} dense />
                                <span className={`truncate font-mono text-[12.5px] ${active ? 'text-txt' : 'text-txt-2 group-hover:text-txt'}`}>
                                  {method.name.split('/').pop()}
                                </span>
                              </motion.button>
                            )
                          })}
                        </div>
                      </motion.div>
                    )}
                  </AnimatePresence>
                </div>
              )
            })}

            {otherRequests.length > 0 && (
              <div className="mt-4">
                <SectionLabel icon={<Cable className="h-3 w-3" />}>Collections</SectionLabel>
                {otherRequests.map((req) => (
                  <RequestRow key={req.id} req={req} onOpen={() => loadRequest(req)} onFav={() => toggleFavorite(req.id)} />
                ))}
              </div>
            )}
          </div>
        </aside>

        {/* Main column */}
        <main className="flex min-w-0 flex-1 flex-col">
          {/* Tab strip */}
          <div className="flex h-10 shrink-0 items-center gap-1 overflow-x-auto border-b border-line bg-ink-850/70 px-2">
            {tabs.map((tab) => {
              const isActive = activeTab === tab.id
              return (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  className={`group flex h-7 items-center gap-2 rounded-lg px-3 text-[13px] transition-colors ${
                    isActive ? 'bg-ink-760 text-txt' : 'text-txt-2 hover:bg-ink-800 hover:text-txt'
                  }`}
                >
                  {tab.method ? (
                    <span className="h-1.5 w-1.5 rounded-full" style={{ background: methodMeta[methodTypeOf(tab.method)].color }} />
                  ) : (
                    <span className="h-1.5 w-1.5 rounded-full bg-txt-3" />
                  )}
                  <span className="max-w-[130px] truncate font-mono">{tab.name}</span>
                  {tabs.length > 1 && (
                    <span onClick={(e) => closeTab(tab.id, e)} className="grid h-4 w-4 place-items-center rounded opacity-0 transition-opacity hover:bg-line-2 group-hover:opacity-100">
                      <X className="h-3 w-3" />
                    </span>
                  )}
                </button>
              )
            })}
            <button onClick={addNewTab} className="grid h-7 w-7 place-items-center rounded-lg text-txt-3 transition-colors hover:bg-ink-800 hover:text-txt">
              <Plus className="h-4 w-4" />
            </button>
          </div>

          {/* Method bar */}
          <div className="flex h-14 shrink-0 items-center gap-3 border-b border-line bg-ink-800/60 px-4">
            <div className="relative min-w-[300px]">
              <select
                value={activeTabData.method}
                onChange={(e) => {
                  const method = Object.values(services).flat().find((m) => m.name === e.target.value)
                  updateTab(activeTab, { method: e.target.value, service: method?.service || '', request: getDefaultRequest(method?.type || 'unary') })
                }}
                className="w-full rounded-lg border border-line bg-ink-900/70 px-3 py-2 font-mono text-sm text-txt focus:border-signal-line focus:outline-none"
              >
                <option value="">Select a method</option>
                {Object.entries(services).map(([service, methods]) => (
                  <optgroup key={service} label={service}>
                    {methods.map((method) => (
                      <option key={method.name} value={method.name}>
                        {method.name.split('/').pop()} — {method.type}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </div>

            <motion.button
              whileTap={{ scale: 0.97 }}
              onClick={handleSend}
              disabled={activeTabData.isLoading || !activeTabData.method}
              className="flex items-center gap-2 rounded-lg bg-signal px-4 py-2 text-sm font-semibold text-ink-900 shadow-signal transition-all hover:brightness-110 disabled:cursor-not-allowed disabled:opacity-40 disabled:shadow-none"
            >
              {activeTabData.isLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" strokeWidth={2.4} />}
              Send
            </motion.button>

            <button
              onClick={() => setShowSaveModal(true)}
              className="flex items-center gap-2 rounded-lg border border-line bg-ink-760 px-3 py-2 text-sm text-txt-2 transition-colors hover:border-line-2 hover:text-txt"
            >
              <Save className="h-4 w-4" />
              Save
            </button>

            {activeTabData.responseTime !== null && (
              <motion.div initial={{ opacity: 0, y: -4 }} animate={{ opacity: 1, y: 0 }} className="ml-auto flex items-center gap-3 font-mono text-[13px]">
                <span className="flex items-center gap-1.5 text-txt-2">
                  <Timer className="h-3.5 w-3.5 text-txt-3" />
                  {activeTabData.responseTime}ms
                </span>
                {activeTabData.status === 'success' ? (
                  <span className="flex items-center gap-1.5 rounded-md border border-signal-line bg-signal-soft px-2 py-0.5 text-signal">
                    <CircleCheck className="h-3.5 w-3.5" /> 200 OK
                  </span>
                ) : (
                  <span className="flex items-center gap-1.5 rounded-md px-2 py-0.5 text-rose-signal" style={{ background: 'rgba(242,116,141,0.1)', borderColor: 'rgba(242,116,141,0.3)', borderWidth: 1 }}>
                    <AlertTriangle className="h-3.5 w-3.5" /> Error
                  </span>
                )}
              </motion.div>
            )}
          </div>

          {/* Request / Response split */}
          <div className="flex min-h-0 flex-1">
            {/* Request */}
            <section className="flex min-w-0 flex-1 flex-col border-r border-line">
              <PanelHeader label="Request">
                {activeTabData.method && <span className="font-mono text-[11px] text-txt-3">{activeTabData.method.split('/').pop()}</span>}
                <button onClick={() => updateTab(activeTab, { request: '{}' })} className="ml-auto font-mono text-[11px] text-txt-3 transition-colors hover:text-txt">
                  clear
                </button>
              </PanelHeader>
              <div className="min-h-0 flex-1 bg-ink-800/40">
                <Editor
                  height="100%"
                  defaultLanguage="json"
                  value={activeTabData.request}
                  onChange={(v) => updateTab(activeTab, { request: v || '{}' })}
                  theme="signal-ink"
                  beforeMount={defineSignalTheme}
                  options={editorOptions}
                />
              </div>
            </section>

            {/* Response */}
            <section className="flex min-w-0 flex-1 flex-col">
              <PanelHeader label="Response">
                {activeTabData.status === 'success' && <span className="font-mono text-[11px] text-signal">200 OK</span>}
                {activeTabData.status === 'error' && <span className="font-mono text-[11px] text-rose-signal">error</span>}
                {activeTabData.isStreaming && (
                  <span className="flex items-center gap-1.5 font-mono text-[11px] text-cyan-signal">
                    <span className="flex items-end gap-0.5">
                      <span className="eq-bar" style={{ animationDelay: '0ms' }} />
                      <span className="eq-bar" style={{ animationDelay: '150ms' }} />
                      <span className="eq-bar" style={{ animationDelay: '300ms' }} />
                    </span>
                    streaming
                  </span>
                )}
                {activeTabData.response && !activeTabData.isStreaming && (
                  <button onClick={copyResponse} className="ml-auto flex items-center gap-1 font-mono text-[11px] text-txt-3 transition-colors hover:text-txt">
                    {copied ? <Check className="h-3 w-3 text-signal" /> : <Copy className="h-3 w-3" />}
                    {copied ? 'copied' : 'copy'}
                  </button>
                )}
              </PanelHeader>
              <div className="min-h-0 flex-1 bg-ink-800/40">
                {activeTabData.isStreaming || activeTabData.streamEvents.length > 0 ? (
                  <div className="h-full space-y-2 overflow-y-auto p-3">
                    {activeTabData.streamEvents.map((event, i) => (
                      <motion.div
                        key={i}
                        initial={{ opacity: 0, y: 8 }}
                        animate={{ opacity: 1, y: 0 }}
                        transition={{ duration: 0.3 }}
                        className="rounded-lg border border-line bg-ink-760/60 p-3"
                      >
                        <div className="mb-1.5 flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-cyan-signal">
                          <ArrowDownToLine className="h-3 w-3" /> message {i + 1}
                        </div>
                        <pre className="whitespace-pre-wrap font-mono text-[12.5px] leading-relaxed text-txt">{event}</pre>
                      </motion.div>
                    ))}
                    {activeTabData.isStreaming && (
                      <div className="flex items-center gap-2 px-1 py-2 font-mono text-[12px] text-cyan-signal">
                        <Loader2 className="h-3.5 w-3.5 animate-spin" /> receiving stream…
                      </div>
                    )}
                  </div>
                ) : activeTabData.response ? (
                  <Editor
                    height="100%"
                    defaultLanguage="json"
                    value={activeTabData.response}
                    theme="signal-ink"
                    beforeMount={defineSignalTheme}
                    options={{ ...editorOptions, readOnly: true }}
                  />
                ) : (
                  <EmptyResponse />
                )}
              </div>
            </section>
          </div>

          {/* Metadata */}
          <AnimatePresence>
            {showMetadata && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: 168, opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ duration: 0.22 }}
                className="shrink-0 overflow-hidden border-t border-line bg-ink-850/70"
              >
                <PanelHeader label="Metadata">
                  <button
                    onClick={() => updateTab(activeTab, { metadata: { ...activeTabData.metadata, '': '' } })}
                    className="ml-auto flex items-center gap-1 font-mono text-[11px] text-txt-3 transition-colors hover:text-txt"
                  >
                    <Plus className="h-3 w-3" /> add
                  </button>
                </PanelHeader>
                <div className="max-h-[128px] space-y-2 overflow-y-auto p-3">
                  {Object.keys(activeTabData.metadata).length === 0 && (
                    <div className="px-1 font-mono text-[12px] text-txt-3">No metadata. gRPC metadata is sent as request headers.</div>
                  )}
                  {Object.entries(activeTabData.metadata).map(([key, value], index) => (
                    <div key={index} className="flex gap-2">
                      <input
                        value={key}
                        onChange={(e) => {
                          const next = { ...activeTabData.metadata }
                          delete next[key]
                          next[e.target.value] = value
                          updateTab(activeTab, { metadata: next })
                        }}
                        className="flex-1 rounded-lg border border-line bg-ink-900/70 px-3 py-1.5 font-mono text-sm text-txt placeholder-txt-3 focus:border-signal-line focus:outline-none"
                        placeholder="key"
                      />
                      <input
                        value={value}
                        onChange={(e) => updateTab(activeTab, { metadata: { ...activeTabData.metadata, [key]: e.target.value } })}
                        className="flex-1 rounded-lg border border-line bg-ink-900/70 px-3 py-1.5 font-mono text-sm text-txt placeholder-txt-3 focus:border-signal-line focus:outline-none"
                        placeholder="value"
                      />
                      <button
                        onClick={() => {
                          const next = { ...activeTabData.metadata }
                          delete next[key]
                          updateTab(activeTab, { metadata: next })
                        }}
                        className="grid h-9 w-9 place-items-center rounded-lg text-txt-3 transition-colors hover:text-rose-signal"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  ))}
                </div>
              </motion.div>
            )}
          </AnimatePresence>

          {/* Instrument status bar */}
          <footer className="flex h-8 shrink-0 items-center gap-4 border-t border-line bg-ink-850/80 px-4 font-mono text-[11px] text-txt-2">
            <span className="flex items-center gap-1.5">
              <span className="signal-dot" style={{ transform: 'scale(0.8)' }} />
              <span className="text-signal">connected</span>
            </span>
            <span className="text-txt-3">·</span>
            <span className="text-txt-2">{serverAddress}</span>
            <span className="flex items-center gap-1 text-txt-3">
              {useTLS ? <Lock className="h-3 w-3 text-amber-signal" /> : <Unlock className="h-3 w-3" />}
              {useTLS ? 'TLS' : 'plaintext'}
            </span>
            <div className="ml-auto flex items-center gap-4">
              {activeTabData.method && (
                <span className="flex items-center gap-1.5">
                  <span className="h-1.5 w-1.5 rounded-full" style={{ background: methodMeta[methodTypeOf(activeTabData.method)].color }} />
                  {methodMeta[methodTypeOf(activeTabData.method)].label}
                </span>
              )}
              <span className="text-txt-3">
                msgs <span className="text-txt-2">{activeTabData.streamEvents.length}</span>
              </span>
              {activeTabData.responseTime !== null && (
                <span className="text-txt-3">
                  latency <span className="text-txt-2">{activeTabData.responseTime}ms</span>
                </span>
              )}
            </div>
          </footer>
        </main>
      </div>

      {/* Save modal */}
      <AnimatePresence>
        {showSaveModal && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
            onClick={() => setShowSaveModal(false)}
          >
            <motion.div
              initial={{ scale: 0.96, y: 8, opacity: 0 }}
              animate={{ scale: 1, y: 0, opacity: 1 }}
              exit={{ scale: 0.96, y: 8, opacity: 0 }}
              transition={{ duration: 0.2, ease: [0.2, 0.7, 0.2, 1] }}
              onClick={(e) => e.stopPropagation()}
              className="w-[380px] overflow-hidden rounded-2xl border border-line-2 bg-ink-800 shadow-panel"
            >
              <div className="flex items-center justify-between border-b border-line px-4 py-3">
                <h3 className="flex items-center gap-2 font-mono text-sm font-semibold text-txt">
                  <Save className="h-4 w-4 text-signal" /> Save request
                </h3>
                <button onClick={() => setShowSaveModal(false)} className="grid h-7 w-7 place-items-center rounded-lg text-txt-3 hover:text-txt">
                  <X className="h-4 w-4" />
                </button>
              </div>
              <div className="p-4">
                <input
                  value={requestName}
                  onChange={(e) => setRequestName(e.target.value)}
                  className="w-full rounded-lg border border-line bg-ink-900/70 px-3.5 py-2.5 text-sm text-txt placeholder-txt-3 focus:border-signal-line focus:outline-none"
                  placeholder="Request name"
                  autoFocus
                  onKeyDown={(e) => e.key === 'Enter' && handleSave()}
                />
              </div>
              <div className="flex justify-end gap-2 px-4 pb-4">
                <button onClick={() => setShowSaveModal(false)} className="rounded-lg px-3.5 py-2 text-sm text-txt-2 transition-colors hover:text-txt">
                  Cancel
                </button>
                <button onClick={handleSave} className="rounded-lg bg-signal px-3.5 py-2 text-sm font-semibold text-ink-900 transition-all hover:brightness-110">
                  Save
                </button>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

/* ── Small presentational helpers ─────────────────────────────────────── */

function Segmented({ active, onClick, title, children }: { active: boolean; onClick: () => void; title: string; children: React.ReactNode }) {
  return (
    <button
      title={title}
      onClick={onClick}
      className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-sm transition-colors ${
        active ? 'border-signal-line bg-signal-soft text-signal' : 'border-line bg-ink-760 text-txt-2 hover:border-line-2 hover:text-txt'
      }`}
    >
      {children}
    </button>
  )
}

function SectionLabel({ icon, children }: { icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-1.5 px-2 py-1.5 font-mono text-[10px] uppercase tracking-[0.18em] text-txt-3">
      {icon}
      {children}
    </div>
  )
}

function RequestRow({ req, onOpen, onFav, favorite }: { req: SavedRequest; onOpen: () => void; onFav: () => void; favorite?: boolean }) {
  return (
    <div className="group flex items-center gap-2 rounded-lg px-2 py-1.5 transition-colors hover:bg-ink-760">
      <button onClick={onFav} className={favorite ? 'text-amber-signal' : 'text-txt-3 transition-colors hover:text-amber-signal'}>
        <Star className={`h-3.5 w-3.5 ${favorite ? 'fill-current' : ''}`} />
      </button>
      <button onClick={onOpen} className="flex-1 truncate text-left text-[13px] text-txt-2 transition-colors group-hover:text-txt">
        {req.name}
      </button>
    </div>
  )
}

function PanelHeader({ label, children }: { label: string; children?: React.ReactNode }) {
  return (
    <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-ink-850/60 px-4">
      <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-txt-3">{label}</span>
      {children}
    </div>
  )
}

function EmptyResponse() {
  return (
    <div className="grid h-full place-items-center">
      <div className="flex flex-col items-center gap-3 text-center">
        <div className="grid h-12 w-12 place-items-center rounded-xl border border-line bg-ink-760">
          <Radio className="h-5 w-5 text-txt-3" />
        </div>
        <div className="font-mono text-[12px] text-txt-3">Send a request to inspect the response</div>
      </div>
    </div>
  )
}

function getDefaultRequest(type_: string): string {
  switch (type_) {
    case 'server_stream':
      return JSON.stringify({ user_id: '', event_types: [] }, null, 2)
    case 'client_stream':
      return JSON.stringify({ user_id: '', metric_name: '', value: 0 }, null, 2)
    case 'bidi_stream':
      return JSON.stringify({ user_id: '', message: '' }, null, 2)
    default:
      return JSON.stringify({}, null, 2)
  }
}
