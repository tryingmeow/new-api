# Development log

## 2026-07-28 — Group retry budget bounded by channel count

- `getChannelQuery` now returns the candidate query together with the number of channels serving the group and model, and reports a group that no longer serves the model as an ordinary no-channel result instead of `数据库一致性被破坏`.
- `GetChannel` and `GetRandomSatisfiedChannel` carry that count to the caller: a retry past the priority ladder is clamped back to the lowest priority and still returns a channel, so callers cannot infer exhaustion from a nil result.
- Auto-group selection dials a group no more times than it has channels for the model, and no more than `RetryTimes+1` times. The channel bound is checked before selection, so an exhausted group costs no attempt; the retry bound is checked after, so the current attempt still uses the group it was chosen from.
- A walk that reaches the end without finding a successor group returns to the first group it gave up on, so a model served by a single channel keeps its retries instead of losing them entirely.
- `priorityRetry` lives on the request context rather than on `RetryParam`, because the distributor and the relay retry loop each build their own param.

## 2026-07-20 — Encrypted NewAPI chat audit archive

- Added authenticated text-chat request and response audit capture for OpenAI, Anthropic, Responses, and Gemini generation endpoints.
- Excluded request headers, query strings, and non-chat binary endpoints; redacted common credential-shaped JSON fields.
- Added bounded response capture with explicit truncation metadata and support for streaming SSE responses.
- Added daily UTC JSONL files containing request, response, user, token, IP, model, channel, status, timing, and correlated request IDs.
- Added a daily systemd archive job that compresses with zstd, encrypts to a single age recipient, uploads to a dedicated OneDrive prefix, verifies remote size, and retains three UTC days locally.
- Remote deletion is intentionally absent. Local deletion requires both a successful upload marker and a fresh remote-size verification.
- By operator choice, the recovery private key is retained in the root-only gateway-pool directory and mirrored to the dedicated OneDrive audit recovery prefix.
- Verified middleware tests, a local encrypted archive/upload/decrypt smoke test, production Docker build, local health, public health, and the active systemd timer.
