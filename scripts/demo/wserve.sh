#!/usr/bin/env bash
export AUTH_API_KEYS=demo-key-1234567890 CLIENT_AUTH_TOKEN=demo-key-1234567890
export TLS_ENABLED=false METRICS_ENABLED=true SERVER_HOST=127.0.0.1
export SERVER_PORT=50551 HEALTH_PORT=18080 METRICS_PORT=19090
export SERVER_STREAM_INTERVAL=250ms SERVER_MAX_STREAM_MESSAGES=4
./bin/server >/tmp/srv.log 2>&1 &
sleep 1.5
echo "server started (gRPC :50551, health :18080, metrics :19090)"
