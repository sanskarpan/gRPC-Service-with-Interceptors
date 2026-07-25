'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { Toaster, toast } from 'react-hot-toast'
import {
  Plus,
  Trash2,
  Save,
  Loader2,
  Send,
  Clock,
  AlertCircle,
  CheckCircle2,
  Server,
  FileJson,
  ChevronDown,
  ChevronRight,
  Folder,
  X,
  Copy,
  Check,
  Activity,
  Key,
  Lock,
  Unlock,
  Star,
  StarOff,
  Search,
  Settings
} from 'lucide-react'
import Editor from '@monaco-editor/react'

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
  name: 'New Request',
  method: '',
  service: '',
  request: '{}',
  response: '',
  metadata: {},
  streamEvents: [],
  status: null,
  responseTime: null,
  isLoading: false,
  isStreaming: false
}

const DEFAULT_REQUESTS: SavedRequest[] = [
  {
    id: '1',
    name: 'Create User',
    method: '/example.UserService/CreateUser',
    service: 'UserService',
    request: JSON.stringify({
      name: "John Doe",
      email: "john@example.com",
      age: 30
    }, null, 2),
    metadata: {},
    favorite: true
  },
  {
    id: '2',
    name: 'Get User',
    method: '/example.UserService/GetUser',
    service: 'UserService',
    request: JSON.stringify({
      id: "user-123"
    }, null, 2),
    metadata: {}
  },
  {
    id: '3',
    name: 'Stream Events',
    method: '/example.UserService/StreamUserEvents',
    service: 'UserService',
    request: JSON.stringify({
      user_id: "user-123",
      event_types: ["login", "logout"]
    }, null, 2),
    metadata: {}
  }
]

const services: { [key: string]: ProtoMethod[] } = {
  UserService: [
    { name: '/example.UserService/GetUser', service: 'UserService', type: 'unary', requestType: 'GetUserRequest', responseType: 'User' },
    { name: '/example.UserService/CreateUser', service: 'UserService', type: 'unary', requestType: 'CreateUserRequest', responseType: 'User' },
    { name: '/example.UserService/UpdateUser', service: 'UserService', type: 'unary', requestType: 'UpdateUserRequest', responseType: 'User' },
    { name: '/example.UserService/DeleteUser', service: 'UserService', type: 'unary', requestType: 'DeleteUserRequest', responseType: 'Empty' },
    { name: '/example.UserService/StreamUserEvents', service: 'UserService', type: 'server_stream', requestType: 'StreamUserEventsRequest', responseType: 'stream UserEvent' },
    { name: '/example.UserService/CollectUserMetrics', service: 'UserService', type: 'client_stream', requestType: 'stream UserMetric', responseType: 'CollectMetricsResponse' },
    { name: '/example.UserService/ChatStream', service: 'UserService', type: 'bidi_stream', requestType: 'stream ChatMessage', responseType: 'stream ChatMessage' },
  ]
}

const methodColors: { [key: string]: string } = {
  unary: 'text-emerald-400 bg-emerald-400/10 border-emerald-400/20',
  server_stream: 'text-blue-400 bg-blue-400/10 border-blue-400/20',
  client_stream: 'text-amber-400 bg-amber-400/10 border-amber-400/20',
  bidi_stream: 'text-purple-400 bg-purple-400/10 border-purple-400/20'
}

const methodLabels: { [key: string]: string } = {
  unary: 'gRPC',
  server_stream: 'Server Stream',
  client_stream: 'Client Stream',
  bidi_stream: 'Bidi Stream'
}

export default function Home() {
  const [serverAddress, setServerAddress] = useState('localhost:50051')
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
      if (copyResetTimeoutRef.current !== null) {
        clearTimeout(copyResetTimeoutRef.current)
      }
      abortControllerRef.current?.abort()
    }
  }, [])

  const activeTabData = tabs.find(t => t.id === activeTab) || tabs[0]

  const updateTab = useCallback((id: string, updates: Partial<Tab>) => {
    setTabs(current => current.map(t => t.id === id ? { ...t, ...updates } : t))
  }, [])

  const addNewTab = useCallback(() => {
    const newTab: Tab = {
      ...DEFAULT_REQUEST,
      id: `tab-${nextTabID.current++}`,
      name: 'New Request'
    }
    setTabs(current => [...current, newTab])
    setActiveTab(newTab.id)
  }, [])

  const closeTab = (id: string, e?: React.MouseEvent) => {
    e?.stopPropagation()
    if (tabs.length === 1) return
    const index = tabs.findIndex(t => t.id === id)
    const newTabs = tabs.filter(t => t.id !== id)
    setTabs(newTabs)
    if (activeTab === id) {
      setActiveTab(newTabs[Math.max(0, index - 1)].id)
    }
  }

  const loadRequest = (req: SavedRequest, asNewTab = false) => {
    const tabData: Tab = {
      ...DEFAULT_REQUEST,
      id: asNewTab ? `tab-${nextTabID.current++}` : activeTab,
      name: req.name,
      method: req.method,
      service: req.service,
      request: req.request,
      metadata: req.metadata
    }
    
    if (asNewTab) {
      setTabs(current => [...current, tabData])
      setActiveTab(tabData.id)
    } else {
      updateTab(activeTab, tabData)
    }
  }

  const handleSend = useCallback(async () => {
    if (!activeTabData.method) {
      toast.error('Please select a method')
      return
    }

    abortControllerRef.current?.abort()
    const controller = new AbortController()
    abortControllerRef.current = controller

    const tabIdAtStart = activeTab

    updateTab(tabIdAtStart, {
      isLoading: true,
      status: null,
      response: '',
      streamEvents: [],
      responseTime: null
    })

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

      // Generate dynamic response based on method
      switch (activeTabData.method) {
        case '/example.UserService/GetUser':
          if (!reqBody.id) {
            throw new Error('id is required')
          }
          mockResponse = {
            id: reqBody.id,
            name: "John Doe",
            email: "john@example.com",
            age: 30,
            created_at: now,
            updated_at: now
          }
          break

        case '/example.UserService/CreateUser':
          if (!reqBody.name) {
            throw new Error('name is required')
          }
          if (!reqBody.email) {
            throw new Error('email is required')
          }
          mockResponse = {
            id: "user-" + Date.now(),
            name: reqBody.name,
            email: reqBody.email,
            age: reqBody.age || 0,
            created_at: now,
            updated_at: now
          }
          break

        case '/example.UserService/UpdateUser':
          if (!reqBody.id) {
            throw new Error('id is required')
          }
          mockResponse = {
            id: reqBody.id,
            name: reqBody.name || "Updated Name",
            email: reqBody.email || "updated@example.com",
            age: reqBody.age || 25,
            created_at: now,
            updated_at: now
          }
          break

        case '/example.UserService/DeleteUser':
          if (!reqBody.id) {
            throw new Error('id is required')
          }
          mockResponse = { success: true, message: "User deleted successfully" }
          break

        case '/example.UserService/StreamUserEvents':
          if (!reqBody.user_id) {
            throw new Error('user_id is required')
          }
          updateTab(tabIdAtStart, { isStreaming: true })
          const events = ['login', 'logout', 'purchase', 'click', 'view']
          for (let i = 0; i < 5; i++) {
            await sleep(200)
            const event = JSON.stringify({
              user_id: reqBody.user_id,
              event_type: events[i % events.length],
              payload: JSON.stringify({ data: i, action: events[i % events.length] }),
              timestamp: new Date().toISOString()
            }, null, 2)
            setTabs(current => current.map(tab => tab.id === tabIdAtStart
              ? { ...tab, streamEvents: [...tab.streamEvents, event] }
              : tab))
          }
          updateTab(tabIdAtStart, { isStreaming: false })
          mockResponse = { message: "Stream completed", events_count: 5 }
          break

        case '/example.UserService/CollectUserMetrics':
          mockResponse = {
            count: 10,
            sum: 500,
            avg: 50
          }
          break

        case '/example.UserService/ChatStream':
          mockResponse = {
            id: "msg-" + Date.now(),
            user_id: reqBody.user_id || "user-1",
            message: reqBody.message || "Hello from server!",
            timestamp: now
          }
          break

        default:
          mockResponse = { message: "Method not fully implemented in demo mode" }
      }

      updateTab(tabIdAtStart, {
        response: JSON.stringify(mockResponse, null, 2),
        responseTime: Date.now() - startTime,
        status: 'success',
        isLoading: false
      })
      toast.success('Request successful!')
    } catch (err: unknown) {
      if (controller.signal.aborted) {
        return
      }
      const message = err instanceof Error ? err.message : 'Request failed'
      updateTab(tabIdAtStart, {
        response: JSON.stringify({ error: message }, null, 2),
        responseTime: Date.now() - startTime,
        status: 'error',
        isLoading: false
      })
      toast.error(message)
    }
  }, [activeTab, activeTabData, updateTab])

  const handleSave = () => {
    if (!requestName.trim()) {
      toast.error('Please enter a name')
      return
    }
    const newRequest: SavedRequest = {
      id: Date.now().toString(),
      name: requestName,
      method: activeTabData.method,
      service: activeTabData.service,
      request: activeTabData.request,
      metadata: activeTabData.metadata
    }
    setSavedRequests(current => [...current, newRequest])
    setShowSaveModal(false)
    setRequestName('')
    toast.success('Request saved!')
  }

  const toggleFavorite = (id: string) => {
    setSavedRequests(current => current.map(r => 
      r.id === id ? { ...r, favorite: !r.favorite } : r
    ))
  }

  const copyResponse = () => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) {
      toast.error('Clipboard not available')
      return
    }
    navigator.clipboard.writeText(activeTabData.response).catch(() => {
      toast.error('Failed to copy response')
    })
    setCopied(true)
    if (copyResetTimeoutRef.current !== null) {
      clearTimeout(copyResetTimeoutRef.current)
    }
    copyResetTimeoutRef.current = setTimeout(() => setCopied(false), 2000)
  }

  const toggleService = (service: string) => {
    const newExpanded = new Set(expandedServices)
    if (newExpanded.has(service)) {
      newExpanded.delete(service)
    } else {
      newExpanded.add(service)
    }
    setExpandedServices(newExpanded)
  }

  const filteredRequests = savedRequests.filter(r => 
    r.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
    r.method.toLowerCase().includes(searchQuery.toLowerCase())
  )

  const favoriteRequests = filteredRequests.filter(r => r.favorite)
  const otherRequests = filteredRequests.filter(r => !r.favorite)

  return (
    <div className="h-screen flex flex-col bg-[#0d1117] text-slate-300">
      <Toaster 
        position="top-right" 
        toastOptions={{
          style: {
            background: '#1e293b',
            color: '#f1f5f9',
            border: '1px solid #334155'
          }
        }}
      />
      
      {/* Top Bar */}
      <header className="h-14 bg-[#161b22] border-b border-[#30363d] flex items-center px-4 gap-4 shrink-0">
          <div className="flex items-center gap-3">
          <div className="w-8 h-8 bg-gradient-to-br from-emerald-500 to-blue-600 rounded-lg flex items-center justify-center">
            <Server className="w-4 h-4 text-white" />
          </div>
          <span className="font-semibold text-white text-lg tracking-tight">gRPC</span>
        </div>
        
        <div className="flex-1 max-w-md mx-4">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
            <input
              type="text"
              value={serverAddress}
              onChange={(e) => setServerAddress(e.target.value)}
              className="w-full bg-[#0d1117] border border-[#30363d] rounded-lg pl-10 pr-4 py-1.5 text-sm text-white placeholder-slate-500 focus:outline-none focus:border-blue-500 transition-colors"
              placeholder="Enter server address..."
            />
          </div>
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={() => setUseTLS(!useTLS)}
            className={`flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm transition-colors ${
              useTLS 
                ? 'bg-amber-500/10 text-amber-400 border border-amber-500/20' 
                : 'bg-[#21262d] text-slate-400 border border-[#30363d] hover:border-slate-500'
            }`}
          >
            {useTLS ? <Lock className="w-3.5 h-3.5" /> : <Unlock className="w-3.5 h-3.5" />}
            TLS
          </button>
          
          <button
            onClick={() => setShowMetadata(!showMetadata)}
            className={`flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm transition-colors ${
              showMetadata 
                ? 'bg-blue-500/10 text-blue-400 border border-blue-500/20' 
                : 'bg-[#21262d] text-slate-400 border border-[#30363d] hover:border-slate-500'
            }`}
          >
            <Key className="w-3.5 h-3.5" />
            Metadata
          </button>

          <div className="w-px h-6 bg-[#30363d]" />
          
          <button className="p-2 text-slate-400 hover:text-white hover:bg-[#21262d] rounded-lg transition-colors">
            <Settings className="w-4 h-4" />
          </button>
        </div>
        <span className="hidden xl:inline-flex items-center rounded-full border border-amber-400/20 bg-amber-400/10 px-2.5 py-1 text-xs text-amber-300">
          Demo mode · responses are simulated locally
        </span>
      </header>

      {/* Main Content */}
      <div className="flex-1 flex overflow-hidden">
        {/* Left Sidebar */}
        <aside className="w-64 bg-[#0d1117] border-r border-[#30363d] flex flex-col shrink-0">
          {/* Search & Actions */}
          <div className="p-3 border-b border-[#30363d]">
            <div className="relative mb-3">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-500" />
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full bg-[#161b22] border border-[#30363d] rounded-lg pl-9 pr-3 py-1.5 text-xs text-slate-300 placeholder-slate-500 focus:outline-none focus:border-blue-500"
                placeholder="Search requests..."
              />
            </div>
            <button
              onClick={addNewTab}
              className="w-full flex items-center justify-center gap-2 bg-[#238636] hover:bg-[#2ea043] text-white rounded-lg py-1.5 text-sm font-medium transition-colors"
            >
              <Plus className="w-4 h-4" />
              New Request
            </button>
          </div>

          {/* Saved Requests */}
          <div className="flex-1 overflow-y-auto">
            {/* Favorites */}
            {favoriteRequests.length > 0 && (
              <div className="p-2">
                <div className="flex items-center gap-2 px-2 py-1 text-xs font-medium text-slate-500 uppercase tracking-wider">
                  <Star className="w-3 h-3" />
                  Favorites
                </div>
                {favoriteRequests.map((req) => (
                  <div
                    key={req.id}
                    className="group flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-[#161b22] cursor-pointer"
                    onClick={() => loadRequest(req)}
                  >
                    <button
                      onClick={(e) => { e.stopPropagation(); toggleFavorite(req.id) }}
                      className="text-amber-400 hover:text-amber-300"
                    >
                      <Star className="w-3.5 h-3.5 fill-current" />
                    </button>
                    <span className="text-sm text-slate-300 truncate">{req.name}</span>
                  </div>
                ))}
              </div>
            )}

            {/* Services Tree */}
            <div className="p-2">
              <div className="flex items-center gap-2 px-2 py-1 text-xs font-medium text-slate-500 uppercase tracking-wider">
                <Server className="w-3 h-3" />
                Services
              </div>
              {Object.keys(services).map((service) => (
                <div key={service}>
                  <div
                    className="flex items-center gap-1 px-2 py-1.5 rounded-lg hover:bg-[#161b22] cursor-pointer"
                    onClick={() => toggleService(service)}
                  >
                    {expandedServices.has(service) ? (
                      <ChevronDown className="w-4 h-4 text-slate-500" />
                    ) : (
                      <ChevronRight className="w-4 h-4 text-slate-500" />
                    )}
                    <Folder className="w-4 h-4 text-blue-400" />
                    <span className="text-sm font-medium text-slate-300">{service}</span>
                  </div>
                  {expandedServices.has(service) && services[service].map((method) => (
                    <div
                      key={method.name}
                      className={`flex items-center gap-2 ml-6 px-2 py-1.5 rounded-lg hover:bg-[#161b22] cursor-pointer ${
                        activeTabData.method === method.name ? 'bg-[#161b22]' : ''
                      }`}
                      onClick={() => updateTab(activeTab, { 
                        method: method.name, 
                        service: method.service,
                        request: getDefaultRequest(method.type)
                      })}
                    >
                      <span className={`text-xs px-1.5 py-0.5 rounded border ${methodColors[method.type]}`}>
                        {method.type === 'unary' ? 'GRPC' : method.type.split('_')[0].slice(0, 6)}
                      </span>
                      <span className="text-sm text-slate-400 truncate">
                        {method.name.split('/').pop()}
                      </span>
                    </div>
                  ))}
                </div>
              ))}

              {/* All Requests */}
              <div className="mt-4">
                <div className="flex items-center gap-2 px-2 py-1 text-xs font-medium text-slate-500 uppercase tracking-wider">
                  <FileJson className="w-3 h-3" />
                  Collections
                </div>
                {otherRequests.map((req) => (
                  <div
                    key={req.id}
                    className="group flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-[#161b22] cursor-pointer"
                    onClick={() => loadRequest(req)}
                  >
                    <button
                      onClick={(e) => { e.stopPropagation(); toggleFavorite(req.id) }}
                      className="text-slate-600 hover:text-amber-400"
                    >
                      <StarOff className="w-3.5 h-3.5" />
                    </button>
                    <span className="text-sm text-slate-400 truncate">{req.name}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </aside>

        {/* Main Panel */}
        <main className="flex-1 flex flex-col min-w-0">
          {/* Tabs */}
          <div className="h-10 bg-[#0d1117] border-b border-[#30363d] flex items-center px-2 gap-1 overflow-x-auto shrink-0">
            {tabs.map((tab) => (
              <div
                key={tab.id}
                className={`flex items-center gap-2 px-3 h-8 rounded-t-lg text-sm cursor-pointer transition-colors group ${
                  activeTab === tab.id 
                    ? 'bg-[#161b22] text-white border-t border-x border-[#30363d]' 
                    : 'text-slate-400 hover:text-slate-300 hover:bg-[#161b22]/50'
                }`}
                onClick={() => setActiveTab(tab.id)}
              >
                {tab.method ? (
                  <span className={`text-xs px-1 py-0.5 rounded ${methodColors[Object.values(services).flat().find(m => m.name === tab.method)?.type || 'unary']}`}>
                    {tab.method.split('/').pop()?.slice(0, 3)}
                  </span>
                ) : (
                  <Activity className="w-3 h-3" />
                )}
                <span className="max-w-[120px] truncate">{tab.name}</span>
                {tabs.length > 1 && (
                  <button
                    onClick={(e) => closeTab(tab.id, e)}
                    className="p-0.5 rounded hover:bg-[#30363d] opacity-0 group-hover:opacity-100 transition-opacity"
                  >
                    <X className="w-3 h-3" />
                  </button>
                )}
              </div>
            ))}
            <button
              onClick={addNewTab}
              className="p-1.5 text-slate-500 hover:text-slate-300 hover:bg-[#161b22] rounded transition-colors"
            >
              <Plus className="w-4 h-4" />
            </button>
          </div>

          {/* Method Bar */}
          <div className="h-14 bg-[#161b22] border-b border-[#30363d] flex items-center px-4 gap-4 shrink-0">
            <select
              value={activeTabData.method}
              onChange={(e) => {
                const method = Object.values(services).flat().find(m => m.name === e.target.value)
                updateTab(activeTab, { 
                  method: e.target.value, 
                  service: method?.service || '',
                  request: getDefaultRequest(method?.type || 'unary')
                })
              }}
              className="bg-[#0d1117] border border-[#30363d] rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500 min-w-[280px]"
            >
              <option value="">Select a method...</option>
              {Object.entries(services).map(([service, methods]) => (
                <optgroup key={service} label={service}>
                  {methods.map((method) => (
                    <option key={method.name} value={method.name}>
                      {method.name.split('/').pop()} ({method.type})
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>

            <button
              onClick={handleSend}
              disabled={activeTabData.isLoading || !activeTabData.method}
              className="flex items-center gap-2 bg-[#238636] hover:bg-[#2ea043] disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg px-4 py-2 text-sm font-medium transition-colors"
            >
              {activeTabData.isLoading ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : (
                <Send className="w-4 h-4" />
              )}
              Send
            </button>

            <button
              onClick={() => setShowSaveModal(true)}
              className="flex items-center gap-2 bg-[#21262d] hover:bg-[#30363d] text-slate-300 rounded-lg px-3 py-2 text-sm border border-[#30363d] hover:border-slate-500 transition-colors"
            >
              <Save className="w-4 h-4" />
              Save
            </button>

            {activeTabData.responseTime !== null && (
              <div className="ml-auto flex items-center gap-2 text-sm">
                <Clock className="w-4 h-4 text-slate-500" />
                <span className="text-slate-400">{activeTabData.responseTime}ms</span>
                {activeTabData.status === 'success' ? (
                  <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                ) : (
                  <AlertCircle className="w-4 h-4 text-red-400" />
                )}
              </div>
            )}
          </div>

          {/* Request/Response Area */}
          <div className="flex-1 flex min-h-0">
            {/* Request Panel */}
            <div className="flex-1 flex flex-col min-w-0 border-r border-[#30363d]">
              <div className="h-8 bg-[#0d1117] border-b border-[#30363d] flex items-center justify-between px-4 shrink-0">
                <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">Request</span>
                <button
                  onClick={() => updateTab(activeTab, { request: '{}' })}
                  className="text-xs text-slate-500 hover:text-slate-300"
                >
                  Clear
                </button>
              </div>
              <div className="flex-1 min-h-0">
                <Editor
                  height="100%"
                  defaultLanguage="json"
                  value={activeTabData.request}
                  onChange={(value) => updateTab(activeTab, { request: value || '{}' })}
                  theme="vs-dark"
                  options={{
                    minimap: { enabled: false },
                    fontSize: 13,
                    lineNumbers: 'on',
                    scrollBeyondLastLine: false,
                    automaticLayout: true,
                    tabSize: 2,
                    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
                    padding: { top: 12 }
                  }}
                />
              </div>
            </div>

            {/* Response Panel */}
            <div className="flex-1 flex flex-col min-w-0">
              <div className="h-8 bg-[#0d1117] border-b border-[#30363d] flex items-center justify-between px-4 shrink-0">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">Response</span>
                  {activeTabData.status === 'success' && (
                    <span className="text-xs text-emerald-400">200 OK</span>
                  )}
                  {activeTabData.status === 'error' && (
                    <span className="text-xs text-red-400">Error</span>
                  )}
                </div>
                {activeTabData.response && (
                  <button
                    onClick={copyResponse}
                    className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300"
                  >
                    {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
                    {copied ? 'Copied!' : 'Copy'}
                  </button>
                )}
              </div>
              <div className="flex-1 min-h-0">
                {activeTabData.isStreaming ? (
                  <div className="h-full overflow-y-auto p-4 space-y-2">
                    {activeTabData.streamEvents.map((event, i) => (
                      <div key={i} className="p-3 bg-[#161b22] rounded-lg border border-[#30363d]">
                        <pre className="text-sm text-emerald-300 whitespace-pre-wrap font-mono">{event}</pre>
                      </div>
                    ))}
                    <div className="flex items-center gap-2 text-blue-400">
                      <Loader2 className="w-4 h-4 animate-spin" />
                      <span className="text-sm">Receiving stream...</span>
                    </div>
                  </div>
                ) : (
                  <Editor
                    height="100%"
                    defaultLanguage="json"
                    value={activeTabData.response || '// Response will appear here'}
                    theme="vs-dark"
                    options={{
                      readOnly: true,
                      minimap: { enabled: false },
                      fontSize: 13,
                      lineNumbers: 'on',
                      scrollBeyondLastLine: false,
                      automaticLayout: true,
                      fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
                      padding: { top: 12 }
                    }}
                  />
                )}
              </div>
            </div>
          </div>

          {/* Metadata Panel */}
          {showMetadata && (
            <div className="h-40 bg-[#0d1117] border-t border-[#30363d] shrink-0">
              <div className="h-8 border-b border-[#30363d] flex items-center justify-between px-4">
                <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">Metadata</span>
                <button
                  onClick={() => updateTab(activeTab, { metadata: { ...activeTabData.metadata, '': '' } })}
                  className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300"
                >
                  <Plus className="w-3 h-3" />
                  Add
                </button>
              </div>
              <div className="p-3 space-y-2 overflow-y-auto max-h-[calc(100%-32px)]">
                {Object.entries(activeTabData.metadata).map(([key, value], index) => (
                  <div key={index} className="flex gap-2">
                    <input
                      type="text"
                      value={key}
                      onChange={(e) => {
                        const newMetadata = { ...activeTabData.metadata }
                        delete newMetadata[key]
                        newMetadata[e.target.value] = value
                        updateTab(activeTab, { metadata: newMetadata })
                      }}
                      className="flex-1 bg-[#161b22] border border-[#30363d] rounded px-3 py-1.5 text-sm text-white placeholder-slate-500 focus:outline-none focus:border-blue-500"
                      placeholder="Key"
                    />
                    <input
                      type="text"
                      value={value}
                      onChange={(e) => updateTab(activeTab, { metadata: { ...activeTabData.metadata, [key]: e.target.value } })}
                      className="flex-1 bg-[#161b22] border border-[#30363d] rounded px-3 py-1.5 text-sm text-white placeholder-slate-500 focus:outline-none focus:border-blue-500"
                      placeholder="Value"
                    />
                    <button
                      onClick={() => {
                        const newMetadata = { ...activeTabData.metadata }
                        delete newMetadata[key]
                        updateTab(activeTab, { metadata: newMetadata })
                      }}
                      className="p-2 text-slate-500 hover:text-red-400"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}
        </main>
      </div>

      {/* Save Modal */}
      {showSaveModal && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 backdrop-blur-sm">
          <div className="bg-[#161b22] rounded-xl w-96 shadow-2xl border border-[#30363d]">
            <div className="p-4 border-b border-[#30363d] flex items-center justify-between">
              <h3 className="text-lg font-semibold text-white">Save Request</h3>
              <button
                onClick={() => setShowSaveModal(false)}
                className="p-1 text-slate-400 hover:text-white"
              >
                <X className="w-5 h-5" />
              </button>
            </div>
            <div className="p-4">
              <input
                type="text"
                value={requestName}
                onChange={(e) => setRequestName(e.target.value)}
                className="w-full bg-[#0d1117] border border-[#30363d] rounded-lg px-4 py-2 text-white placeholder-slate-500 focus:outline-none focus:border-blue-500"
                placeholder="Request name"
                autoFocus
                onKeyDown={(e) => e.key === 'Enter' && handleSave()}
              />
            </div>
            <div className="p-4 pt-0 flex gap-2 justify-end">
              <button
                onClick={() => setShowSaveModal(false)}
                className="px-4 py-2 text-slate-400 hover:text-white transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleSave}
                className="px-4 py-2 bg-[#238636] hover:bg-[#2ea043] text-white rounded-lg transition-colors"
              >
                Save
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function getDefaultRequest(type_: string): string {
  switch (type_) {
    case 'server_stream':
      return JSON.stringify({ user_id: "", event_types: [] }, null, 2)
    case 'client_stream':
      return JSON.stringify({ user_id: "", metric_name: "", value: 0 }, null, 2)
    case 'bidi_stream':
      return JSON.stringify({ user_id: "", message: "" }, null, 2)
    default:
      return JSON.stringify({}, null, 2)
  }
}
