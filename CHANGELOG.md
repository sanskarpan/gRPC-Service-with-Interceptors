# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial gRPC service with UserService CRUD operations
- Authentication via JWT and API keys
- TLS/mTLS support
- Rate limiting, timeouts, and resource limits
- Streaming RPCs (server, client, bidirectional)
- Metrics, structured logging, and distributed tracing
- PostgreSQL storage with automatic migration
- Comprehensive E2E, load, and soak tests
- Docker, Docker Compose, and Kubernetes deployment
- CI/CD pipeline with linting, vulnerability scanning, and container build