// The two addresses the MCP · SSO card shows, computed the way the server
// computes them so what the administrator copies is what the server claims.

// mcpResource is the resource identifier: what is written, else the OIDC
// public address, and only then this browser's origin — the same order the
// server decides in, because a Host header is anybody's to set.
export function mcpResource(
  resource: string,
  publicUrl: string,
  origin: string,
) {
  const written = resource.trim();
  if (written) return written;
  const base = publicUrl.trim().replace(/\/+$/, "");
  return (base || origin) + "/mcp";
}

// mcpMetadataUrl is the RFC 9728 document on the resource's own origin.
export function mcpMetadataUrl(resource: string) {
  try {
    const url = new URL(resource);
    return url.origin + "/.well-known/oauth-protected-resource/mcp";
  } catch {
    return (
      resource.replace(/\/mcp$/, "") +
      "/.well-known/oauth-protected-resource/mcp"
    );
  }
}
