# Public access via Cloudflare Tunnel

If you don't want to SSH-tunnel every time (especially from phones or
collaborators' machines), front `roost` with a [Cloudflare Tunnel] and
[Cloudflare Access]. You get a public HTTPS URL with zero open ports
on your machine, TLS terminated at the edge, and email/SSO auth in
front of the password.

```
[ your phone / laptop / anywhere ]
            ↓
   https://roost.example.com   (Cloudflare Edge: TLS + Access SSO)
            ↓
       Cloudflare Tunnel       (outbound from your host)
            ↓
         cloudflared           (running alongside roost)
            ↓
       http://127.0.0.1:8080   (roost — never exposed to a public port)
```

## Setup

1. **Keep `roost` on loopback** — no config change needed:

   ```toml
   [server]
   addr = "127.0.0.1:8080"
   ```

2. **Add a Public Hostname to your existing tunnel** (Cloudflare Zero
   Trust → Networks → Tunnels → \[your tunnel\] → Public Hostname → Add):

   | Field    | Value                          |
   |----------|--------------------------------|
   | Subdomain | `roost` (or whatever)         |
   | Domain    | your domain                   |
   | Service   | Type `HTTP`, URL `localhost:8080` |
   | Additional → TLS | check **No TLS Verify** |

3. **Put Cloudflare Access in front** (strongly recommended — without
   it, anyone on the internet who finds your hostname is exposed to
   `roost`'s single password). Zero Trust → Access → Applications →
   Add → Self-hosted:

   - Application: `roost`
   - Domain: `roost.example.com`
   - Policy: Allow, with rule **Emails → include → your address**

   First page load now requires an email OTP or your SSO. roost's
   password becomes a second factor behind that.

## Compatibility notes

| Feature | Through CF Tunnel |
|---------|--------------------|
| Terminal WebSocket / SSE notifications | ✅ tunnels support WS natively |
| HTTPS                              | ✅ Cloudflare provides the cert |
| `Stop`-hook POST to `/api/notify`  | ✅ Hooks hit `127.0.0.1:8080` locally, never via the tunnel |
| Drag-drop / Ctrl+V upload          | ⚠️ Cloudflare's request body limit is **100 MB on Free, 200 MB on Pro, 500 MB on Business**. For larger transfers, fall back to SSH tunnel (`ssh -L 8080:localhost:8080`) which has no such limit. |
| Streaming download of multi-GB files | ✅ usually fine; falls back to a direct `<a download>` link past 2 GB |

[Cloudflare Tunnel]: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/
[Cloudflare Access]: https://developers.cloudflare.com/cloudflare-one/applications/
