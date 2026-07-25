'use client'

import { useEffect } from 'react'

export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  useEffect(() => {
    if (typeof console !== 'undefined') {
      console.error('Application error boundary caught:', error)
    }
  }, [error])

  return (
    <div className="flex h-screen items-center justify-center px-6 bg-[#0d1117] text-slate-300">
      <div className="max-w-md text-center">
        <h1 className="text-2xl font-semibold text-white">
          Something went wrong
        </h1>
        <p className="mt-3 text-sm text-slate-400">
          The gRPC client encountered an unexpected error. Reloading will
          discard unsaved work.
        </p>
        {error.digest && (
          <p className="mt-2 font-mono text-xs text-slate-500">
            digest: {error.digest}
          </p>
        )}
        <button
          onClick={reset}
          className="mt-6 rounded-lg bg-[#238636] px-4 py-2 text-sm font-medium text-white hover:bg-[#2ea043]"
        >
          Try again
        </button>
      </div>
    </div>
  )
}
