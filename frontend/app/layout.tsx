import type { Metadata } from 'next'
import './globals.css'

export const metadata: Metadata = {
  title: 'gRPC Client',
  description: 'A modern gRPC testing client',
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="en" className="dark">
      <body className="antialiased">{children}</body>
    </html>
  )
}
