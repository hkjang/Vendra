-- MCP over SSO: the /mcp endpoint may accept a Keycloak access token beside
-- the personal key (internal/httpapi/mcpoauth.go). Installed off. The names
-- are the ones every service in the company uses for the same four settings,
-- so an operator who has configured one has configured them all; the issuer
-- and client id are the web sign-in's, in the `oidc` row, and are not repeated.
--
-- mcp.oauth.scopes is what a token holder may do, stated once by the
-- administrator rather than taught to Keycloak: the seven read permissions the
-- MCP tools check, and no amount fields. Held to the holder's own role at
-- every request, exactly as a personal key's scopes are.
INSERT INTO settings(key,value,category) VALUES
 ('mcp.oauth.enabled','false','identity'),
 ('mcp.oauth.resource','""','identity'),
 ('mcp.oauth.audience','""','identity'),
 ('mcp.oauth.scopes','"supplier.read contract.read purchase_order.read issue.read risk.read evaluation.read spend.read"','identity')
ON CONFLICT(key) DO NOTHING;
