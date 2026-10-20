# Development log

## 2026-10-20 — Merge upstream rc.45 and cover Responses WebSocket audit

- Merged upstream `6cf0d90b5ff27f901697dddd61e0c28aba8efefc` with the local candidate-count return contract and auto-group retry state intact. Database routing now applies upstream normalized-model fallback before counting the filtered candidates, matching memory-cache routing.
- The new Responses WebSocket path records sanitized application events per turn, with separate configured request/response byte caps. A final bounded record contains frame/byte totals and terminal classification after the client terminal write and request middleware complete. Events outside an admitted turn flush at session close; headers, query credentials and upstream connection state are excluded.
- HTTP audit omits malformed/incomplete JSON rather than saving unredacted prefixes. SSE audit sanitizes each JSON data event, including credential-key variants, while retaining normal streaming content. Existing HTTP, Gemini and task-plugin hooks and encrypted archive scripts are preserved.
- Verified full migration twice on fresh databases and twice on a synthetic previous-release fixture with legacy tokens/quotas, token fingerprint uniqueness, and filtered normalized routing with cache on/off across SQLite, MySQL 8.0.46 and PostgreSQL 16.15. All root-module Go package suites passed, along with focused retry/audit/websocket regressions, the root server build, independent `relaykit` build, and frontend `build:check`.
- Audit security review references: OWASP [Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html), [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html), and [Logging — data to exclude](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html#data-to-exclude). Focused tests cover credential exclusion, malformed/truncated payloads, and per-turn capture bounds; this is not an application-wide compliance assessment.
- No deployment, production database/configuration change or remote push was performed.

## 2026-09-24 — Chat audit records `/v1/responses` again

- From the 2026-09-12 deploy until this fix, chat audit recorded nothing. Upstream `eb48396d5` (sandboxed JS plugin system) moved `POST /v1/responses` out of the relay `httpRouter` group, which carries `ChatAudit()`, into an engine-level task-plugin chain in `router/task-plugin-protocol-router.go` that never had it. The hook lines in `relay-router.go` survived the merge unchanged; the route moved out from under them. Codex traffic is almost all `/v1/responses`, so the audit directory stayed empty and no error was logged, because the middleware never ran.
- `ChatAudit()` now sits in the `openai_responses.create` chain after auth and rate limiting and before channel selection, the same position it held before the merge.
- `router/chat_audit_router_test.go` builds the engine the way `SetRouter` does and posts to every `isChatAuditPath` path, plus `/v1/embeddings` as a negative control. It fails if upstream moves a chat route out of audit again. It lives in its own file so upstream merges do not conflict on it.
- Records from 2026-09-12 to 2026-09-24 do not exist and cannot be recovered. The OneDrive archive covers 2026-08-29 to 2026-09-12.
- Rollback image: `local/new-api:pre-chataudit-fix-20260924`. Verified after deploy: container healthy, one authenticated `/v1/responses` call produced a record with request, response, token name and status 200, and no bearer key in the file.

## 2026-09-12 — Synced with upstream and shipped

- Merged `origin/main` after being 107 commits behind (upstream head `385d2dfd1`, 2026-09-11). Conflicts were confined to the selection layer — `model/ability.go`, `model/channel_cache.go`, `service/channel_select.go` and `service/channel_select_test.go` — and were resolved by taking upstream wholesale; the local retry bound is re-applied as its own commit on top. The chat-audit middleware merged untouched and both router hook points survive upstream unchanged.
- Upstream fixes this sync was worth doing for: `1751f43ee` SQLite WAL + busy timeout + `_txlock=immediate` (the pure-Go driver silently ignored `_busy_timeout=`, leaving this deployment on SQLite's 5s default), `b518d0033` bounded wait for upstream response headers (an unbounded park pinned three copies of the request body per stuck request), `9a8674425` no redundant schema migrations on restart, `6b659fd61` reasoning effort no longer implicitly remapped, `b148af80` response-injection fix in the unroutable-model error body.
- `4fc9d1f1f` rebuilds the `options` primary key and dedupes last-wins. This instance had 15 rows and 15 distinct keys, so the migration was a detected no-op: no `options_legacy_*` table was created and the hand-written `ModelRatio`/`CompletionRatio`/`CacheRatio` blobs came through byte-identical.
- `3a9f41ee8` disables `/v1/messages/count_tokens` upstream. Not a regression here: the endpoint was added upstream after this fork's base and never existed in this tree.
- Backups taken before the migrations ran, all under `/home/new-api-backups/`: `one-api-20260912-pre-upstream-sync.db.gz` (online `.backup`, WAL-safe), the three pricing blobs, `channel_affinity_setting.rules`, and `pricing-20260912-pre-upstream-sync.json` from `/api/pricing`.
- Rollback points: branch `pre-upstream-sync-20260912` and image `local/new-api:known-good-20260827`. The shipped image is also tagged `local/new-api:source-20260912`.
- Verified after deploy: container healthy, no errors in the log, `journal_mode=wal`, `/api/pricing` identical at 61 models with zero ratio changes and nothing fallen to the `37.5` fallback, `channel_affinity_setting.rules` byte-identical with both CLI rules still at `skip_retry_on_failure=false`, and the 15 channels' enable/disable state preserved (channels 8 and 11 still disabled).

## 2026-09-12 — Re-applied the group retry bound on upstream's rewritten selection layer

- Merging 107 upstream commits took `model/ability.go`, `model/channel_cache.go` and `service/channel_select.go` wholesale, which dropped the 2026-07-28 fix. The clamp it removed was back: `retry >= len(priorities)` fell to the lowest priority tier and still returned a channel, so with `RetryTimes=3` a group holding one channel burned four attempts on it before the next auto-group was reached.
- The count now comes straight out of `GetChannel` and `GetRandomSatisfiedChannel`, which return `(*Channel, int, error)`. The 2026-07-28 entry's `getChannelQuery` mechanism no longer applies: upstream rewrote `GetChannel` to filter abilities in Go, and `getChannelQuery`/`getPriority` are dead code with no callers.
- Both paths count the same filtered candidate set the selection draws from, so the count and the selection cannot disagree: the SQL path counts filtered ability rows (one row per channel within a group and model), the cache path counts the filtered candidate id list.
- Auto-group selection keeps the same two bounds as before — a group is dialled no more times than it has channels for the model, and no more than `RetryTimes+1` times — and keeps the `givenUpChannel` fallback, so a model served by a single channel in a single group keeps its full retry budget instead of losing it.
- `priorityRetry` lives on the request context rather than on `RetryParam`, because the distributor and the relay retry loop each build their own param.

## 2026-08-27 — Synced with upstream and shipped the local fork

- Merged `origin/main` into the local fork after being 114 commits behind. No textual conflicts: the merge kept both the chat-audit hooks in `router/relay-router.go` and the channel-selection changes.
- Upstream's per-token Auto groups (#6590) added a third condition for a group to count as selectable — it must carry a group ratio, not just be usable and listed in Auto. Only the auto-group test fixture relied on the old two-condition rule, so the fixture now registers ratios; the production path needed no change.
- Backed up the SQLite volume before the upstream migrations ran, to `/home/new-api-backups/newapi-data-20260827-pre-upstream-sync.tar.gz`. Taken with the container stopped, so the WAL was already checkpointed into `one-api.db`.
- Rollback points: branch `pre-upstream-sync-20260827` and image `local/new-api:rollback-20260827`.
- Verified after deploy: 33 backend packages green, migration clean, container healthy, frontend serving, and the data intact at 15 channels, 2 users, 4 tokens, 338 abilities, 15824 logs.
- `/home/makefile` gained `newapi-build` and `newapi-deploy`; `newapi-restart` on its own reuses the existing image and does not pick up source changes.

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
