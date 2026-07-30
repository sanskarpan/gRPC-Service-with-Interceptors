#!/usr/bin/env bash
curl -s http://127.0.0.1:19090/metrics | grep -E '^grpc_server_(total_requests|active_streams)' | head -5
pkill -f 'bin/server' 2>/dev/null || true
