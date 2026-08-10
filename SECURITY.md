# Security Policy

## Trust model — read this before deploying

**NexWiki has no authentication.** There are no accounts, no passwords, and no API tokens. Anyone who can reach the port has full read, write, and delete access to every article — and to every MCP tool, including `delete_wiki_article`, `import_okf_bundle`, and `export_okf_bundle`.

NexWiki is designed for **a single user on a trusted machine or private network**. That is the supported deployment.

> ⚠️ **Do not expose NexWiki directly to the public internet.** The reverse-proxy examples in the [README](./README.md#3-setting-up-ssl--reverse-proxy-caddy--nginx) terminate TLS; they do **not** add access control. If you need NexWiki reachable from outside your network, put it behind something that authenticates — a VPN (Tailscale, WireGuard), an identity-aware proxy, or your reverse proxy's own auth (e.g. Caddy `basic_auth`, `oauth2-proxy`).

Multi-user support with accounts and per-user permissions is planned as a separate, explicitly enterprise-oriented variant. It does not exist today.

## Browser origin protection

Because NexWiki is unauthenticated and typically runs on `localhost`, any website you visit in the same browser could otherwise reach it. NexWiki therefore validates the browser `Origin` header on every request and rejects unknown origins with `403`. Allowed by default:

- Requests with **no `Origin` header** — non-browser clients such as `curl`, MCP SDKs, and native apps.
- **Loopback origins** — the wiki's own UI and the Vite dev server on `:5173`.
- **Same-origin requests where the host is a loopback address or a bare IP** — for example reaching the wiki from your phone at `http://192.168.1.50:8080`.

If you serve NexWiki from a DNS name (a reverse-proxied domain), name that origin explicitly:

```bash
NEXWIKI_ALLOWED_ORIGINS="https://wiki.example.com"
```

Multiple origins are comma-separated. Setting `NEXWIKI_ALLOWED_ORIGINS="*"` restores the old permissive behavior — **this is unsafe on any machine that also browses the web** and exists only as an escape hatch.

DNS names are deliberately not auto-trusted via the same-origin rule: that would let a DNS-rebinding attack satisfy `Origin == Host` and reach your wiki.

## Other hardening in place

- Uploaded SVGs are served with `Content-Disposition: attachment` and a restrictive CSP, so they cannot execute as same-origin scripts. Inline `<img>` embedding still works.
- Upload MIME types must agree with the file extension, and all assets are served with `X-Content-Type-Options: nosniff`.
- Search snippets are HTML-escaped before rendering.
- The HTTP server sets read and idle timeouts.

## Agent attribution is not authentication

The activity log records an `agent` for every change, `get_article_history` reports who made each revision, and the Activity drawer lets you filter by agent. **None of this is an identity claim.**

The value comes from the MCP client's self-reported `clientInfo`, or from the server's configured `NEXWIKI_AGENT_NAME` for clients that report nothing. Any client can send any name. Since NexWiki is unauthenticated (see the trust model above), attribution is a convenience for telling *your own* agents apart — not evidence of who made a change, and not something to build an access decision on.

Self-reported names are length-capped and stripped of control characters before they reach the log, so a hostile value cannot corrupt the record or the UI that renders it.

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue.

Use [GitHub's private vulnerability reporting](https://github.com/gruberchris/nexwiki/security/advisories/new) for this repository.

Include: what you found, how to reproduce it, and the impact you believe it has. You can expect an initial response within about a week. Once a fix is available, you will be credited in the advisory unless you prefer otherwise.

## Supported versions

NexWiki is pre-1.0 and under active development. Fixes land on `main` and ship in the next release; older releases are not patched.
