# Chat audit storage

Production chat requests are recorded by the authenticated relay middleware for these text endpoints:

- `/v1/chat/completions`
- `/v1/completions`
- `/v1/messages`
- `/v1/responses`
- `/v1/responses/compact`
- Gemini `generateContent` and `streamGenerateContent`

Authorization headers and URL queries are never stored. Common credential-shaped JSON fields such as `api_key`, `access_token`, `password`, and `client_secret` are replaced with `[REDACTED]`. Image, audio, embedding, and moderation endpoints are excluded. Each request and response is capped by `CHAT_AUDIT_MAX_BODY_MB`; truncation is explicit in the record.

## Storage lifecycle

- Container path: `/data/chat-audit/audit-YYYY-MM-DD.jsonl`
- Host path: `/var/lib/docker/volumes/newapi_newapi-data/_data/chat-audit/`
- Daily timer: `newapi-audit-archive.timer`, scheduled for 03:15 UTC with up to five minutes of jitter
- Remote path: `e5onedrive:newapi-audit/YYYY/MM/DD/`
- Archive format: `audit-YYYY-MM-DD.jsonl.zst.age` plus a SHA-256 manifest
- Local retention: the current UTC day and two previous UTC days
- Remote retention: indefinite; the archive job never deletes OneDrive objects

The archive job retries every unarchived closed daily file. It writes a success marker only after the OneDrive object size is verified. Local data older than the retention window is deleted only when that marker exists and the remote size is verified again.

## Recovery key

The server archives with an age public recipient. By operator choice, the corresponding private key is retained at `/home/manage-ai-gateway-pool/newapi-audit-recovery-key.txt` and mirrored in plaintext at `e5onedrive:newapi-audit/recovery/newapi-audit-recovery-key.txt`.

Verify and restore an archive:

```bash
sha256sum -c audit-YYYY-MM-DD.jsonl.zst.age.sha256
age -d -i newapi-audit-recovery-key.txt audit-YYYY-MM-DD.jsonl.zst.age | zstd -d -c > audit-YYYY-MM-DD.jsonl
```

## Operations

```bash
systemctl status newapi-audit-archive.timer
systemctl start newapi-audit-archive.service
journalctl -u newapi-audit-archive.service
```

The runtime configuration is `/etc/newapi-audit.env`. Setting `CHAT_AUDIT_ENABLED` to `false` in `compose.local.yml` and recreating the NewAPI container disables collection without deleting existing records.
