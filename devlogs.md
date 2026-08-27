# Development log

## 2026-07-20 — Encrypted NewAPI chat audit archive

- Added authenticated text-chat request and response audit capture for OpenAI, Anthropic, Responses, and Gemini generation endpoints.
- Excluded request headers, query strings, and non-chat binary endpoints; redacted common credential-shaped JSON fields.
- Added bounded response capture with explicit truncation metadata and support for streaming SSE responses.
- Added daily UTC JSONL files containing request, response, user, token, IP, model, channel, status, timing, and correlated request IDs.
- Added a daily systemd archive job that compresses with zstd, encrypts to a single age recipient, uploads to a dedicated OneDrive prefix, verifies remote size, and retains three UTC days locally.
- Remote deletion is intentionally absent. Local deletion requires both a successful upload marker and a fresh remote-size verification.
- By operator choice, the recovery private key is retained in the root-only gateway-pool directory and mirrored to the dedicated OneDrive audit recovery prefix.
- Verified middleware tests, a local encrypted archive/upload/decrypt smoke test, production Docker build, local health, public health, and the active systemd timer.
