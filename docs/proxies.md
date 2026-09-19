# Running behind a reverse proxy

A drive with no `DRIVE_HOSTNAME` serves plaintext and expects something in front of it. That
something terminates TLS and forwards to `127.0.0.1:8080`, and the only part it can get wrong is
uploads: a browser sends a file in 8 MiB chunks over one long request each, and proxy defaults are
written for form posts.

Measured against the real thing — `DRIVE_PROXY_TEST=1 go test ./cmd/drive -run Proxy` runs each
proxy in a container in front of a real instance, interrupts an upload and resumes it.

## Caddy — works unconfigured

```
drive.example.com {
  reverse_proxy 127.0.0.1:8080
}
```

No body size limit, and the request body is streamed rather than buffered. An upload interrupted
halfway leaves the server holding what actually arrived, so the resume sends only the rest. This is
the configuration the resumption story was designed against.

## nginx — needs one line

```nginx
server {
  listen 443 ssl;
  server_name drive.example.com;

  location / {
    proxy_pass http://127.0.0.1:8080;

    client_max_body_size 0;        # without this, 1 MiB, and every upload chunk is refused 413
    proxy_request_buffering off;   # optional: resume from where the transfer really stopped
    proxy_read_timeout 300s;       # optional: a slow chunk is not a dead connection
  }
}
```

- **`client_max_body_size` defaults to 1 MiB.** An 8 MiB chunk is answered `413` before nginx
  forwards anything, so uploads of anything larger fail outright until this is raised. `0` removes
  the limit; the drive has its own disk-space guard behind it. The drive never sends a 413 itself —
  it has no request size limit — so the interface says so in as many words when it sees one, naming
  this setting. It does not shrink its chunks to fit: that is configuration, and configuration
  belongs to whoever wrote it.
- **`proxy_request_buffering` defaults to on**, so nginx buffers the whole chunk before contacting
  the drive. Nothing is lost — the client re-sends from the offset the drive reports, which is then
  simply the start of the chunk — but a connection that dies at 90% costs the whole chunk again.
  With buffering off, nginx keeps forwarding what it had already buffered *after* the browser has
  gone, so the offset the drive reports can still be moving when the resume arrives. The drive
  answers that with `409` and its current offset and the client re-reads it, which is exactly why
  the offset comes from the server and never from the client's own count.

## Cloudflare — untested here

Not covered by the test above, which would need an account, a public hostname and a tunnel. From
Cloudflare's own documentation, the two defaults that matter:

- a **100 MB request body limit** on the free plan, well above the 8 MiB chunk size, so uploads of
  any size should pass;
- a **100-second proxy timeout**, which a single chunk on a slow link can exceed. That is a dropped
  connection, which is the case tus resumption exists for; the upload continues from the offset.

Treat both as unverified until someone runs an upload through a Cloudflare-fronted instance.
