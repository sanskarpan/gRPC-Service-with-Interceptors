export default function Loading() {
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex h-screen items-center justify-center bg-[#0d1117] text-slate-400"
    >
      <div className="flex items-center gap-3 text-sm">
        <span className="h-2 w-2 animate-pulse rounded-full bg-emerald-400" />
        Loading gRPC client...
      </div>
    </div>
  )
}
