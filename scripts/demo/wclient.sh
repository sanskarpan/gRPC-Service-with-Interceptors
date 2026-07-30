#!/usr/bin/env bash
CLIENT_ADDRESS=127.0.0.1:50551 ./bin/client 2>&1 \
  | grep -oE '"message":"[^"]+"' | sed 's/"message":"/  - /; s/"$//' | head -14
