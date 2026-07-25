#!/usr/bin/env bash

set -euo pipefail
umask 077

cert_dir=${1:-certs}
mkdir -p "$cert_dir"

openssl req -x509 -newkey rsa:4096 \
  -keyout "$cert_dir/ca.key" -out "$cert_dir/ca.crt" \
  -days 365 -nodes -subj "/CN=gRPC development CA" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:1" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"

openssl req -newkey rsa:4096 -nodes \
  -keyout "$cert_dir/server.key" -out "$cert_dir/server.csr" \
  -subj "/CN=grpc-server"
openssl x509 -req -in "$cert_dir/server.csr" \
  -CA "$cert_dir/ca.crt" -CAkey "$cert_dir/ca.key" -CAcreateserial \
  -out "$cert_dir/server.crt" -days 365 \
  -extfile <(printf '%s\n' \
    'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature,keyEncipherment' \
    'extendedKeyUsage=serverAuth' \
    'subjectAltName=DNS:localhost,DNS:grpc-server,IP:127.0.0.1')

openssl req -newkey rsa:4096 -nodes \
  -keyout "$cert_dir/client.key" -out "$cert_dir/client.csr" \
  -subj "/CN=grpc-client"
openssl x509 -req -in "$cert_dir/client.csr" \
  -CA "$cert_dir/ca.crt" -CAkey "$cert_dir/ca.key" -CAcreateserial \
  -out "$cert_dir/client.crt" -days 365 \
  -extfile <(printf '%s\n' \
    'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature,keyEncipherment' \
    'extendedKeyUsage=clientAuth')

rm -f "$cert_dir"/*.csr "$cert_dir"/*.srl
openssl verify -CAfile "$cert_dir/ca.crt" "$cert_dir/server.crt" "$cert_dir/client.crt"
echo "Development certificates generated in $cert_dir (keep private keys out of source control)."
