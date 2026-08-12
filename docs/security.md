# Security model

## Authentication

- Local password: bcrypt
- OIDC: Discovery, Authorization Code, PKCE S256, state, nonce, issuer/audience/signature verification
- Session: random 256-bit bearer, database에는 SHA-256 hash만 저장, HttpOnly/SameSite cookie
- API key: `vnd_` prefix, 생성 시 한 번만 표시, hash 저장, Scope/만료/폐기/회전

## Authorization

- RBAC permissions support exact, domain wildcard(`supplier.*`) and read wildcard(`*.read`).
- Data Scope is `own`, `department`, `division`, `company`.
- Supplier Portal requests are forced to the user's supplier ID regardless of client input.
- Amount fields are removed unless the Principal has the domain amount permission or wildcard.
- `access_grants` supports resource conditions, valid-from/until and delegated-by for temporary access/delegation.
- Workflow step role and object conditions enforce approval authority.

## Encryption and audit

OIDC/AI secrets and bank accounts use AES-256-GCM with versioned ciphertext and authenticated context. `ENCRYPTION_KEY` is never stored in PostgreSQL. Audit events include actor, timestamp, action, object, before/after JSON, IP, session and request ID.

Document upload limits size, strips paths from filenames, assigns an opaque storage name, records SHA-256 and validates that downloads stay under the configured storage root.

## Reverse proxy

Use an allow-listed reverse proxy and TLS. Vendra emits CSP, frame denial, MIME sniffing protection, referrer and browser permissions policies. Set `secureCookie` in service administration after TLS is enabled.
