import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MCPOAuthSettings } from "./Admin";
import { mcpMetadataUrl, mcpResource } from "../mcpOauth";
import { APIError, put } from "../api";

// The wrappers close over the real api, so the one the card writes through
// is mocked by name or a form submit silently bypasses the mock.
vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  put: vi.fn(),
}));

const save = vi.mocked(put);

const row = (key: string, value: unknown, category = "identity") => ({
  key,
  value,
  secret: false,
  secretConfigured: false,
  category,
  updatedAt: "2026-09-17T03:00:00Z",
});

const configured = (extra: Record<string, unknown> = {}) => [
  row("oidc", {
    enabled: true,
    issuer: "https://keycloak.example/realms/company",
    clientId: "vendra-web",
    publicUrl: "https://vendra.example.co.kr/",
  }),
  row("mcp.oauth.enabled", false),
  row("mcp.oauth.resource", ""),
  row("mcp.oauth.audience", ""),
  row("mcp.oauth.scopes", "supplier.read contract.read"),
  ...Object.entries(extra).map(([key, value]) => row(key, value)),
];

const written = (key: string) =>
  save.mock.calls.find(
    ([path]) => path === "/api/v1/admin/settings/" + key,
  )?.[1];

describe("MCPOAuthSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    save.mockResolvedValue({ ok: true });
  });

  it("names the addresses a client needs, from the OIDC public address", () => {
    render(
      <MCPOAuthSettings
        settings={configured()}
        notify={() => {}}
        reload={() => {}}
      />,
    );
    expect(
      screen.getByText("https://vendra.example.co.kr/mcp"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "https://vendra.example.co.kr/.well-known/oauth-protected-resource/mcp",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("비활성")).toBeInTheDocument();
  });

  it("writes the four rows under the standard names, the switch last", async () => {
    const notify = vi.fn();
    const reload = vi.fn();
    render(
      <MCPOAuthSettings
        settings={configured()}
        notify={notify}
        reload={reload}
      />,
    );
    fireEvent.click(screen.getByLabelText(/MCP 를 SSO 토큰으로 열기/));
    fireEvent.change(screen.getByPlaceholderText("claude-mcp cursor-mcp"), {
      target: { value: "claude-mcp" },
    });
    fireEvent.change(
      screen.getByPlaceholderText("https://vendra.example.co.kr/mcp"),
      {
        target: { value: "https://mcp.vendra.example.co.kr/mcp" },
      },
    );
    fireEvent.click(screen.getByRole("button", { name: /MCP SSO 설정 저장/ }));
    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(written("mcp.oauth.enabled")).toEqual({
      category: "identity",
      value: true,
    });
    expect(written("mcp.oauth.audience")).toEqual({
      category: "identity",
      value: "claude-mcp",
    });
    expect(written("mcp.oauth.resource")).toEqual({
      category: "identity",
      value: "https://mcp.vendra.example.co.kr/mcp",
    });
    expect(written("mcp.oauth.scopes")).toEqual({
      category: "identity",
      value: "supplier.read contract.read",
    });
    expect(save.mock.calls.at(-1)?.[0]).toBe(
      "/api/v1/admin/settings/mcp.oauth.enabled",
    );
    expect(notify).toHaveBeenCalledWith("MCP SSO 설정을 저장했습니다.");
  });

  it("shows the server's refusal instead of throwing it", async () => {
    save.mockImplementation((path: string) =>
      path.endsWith("mcp.oauth.scopes")
        ? Promise.reject(
            new APIError(
              400,
              'mcp.oauth.scopes 의 "mcp:read"는 이 시스템이 확인하지 않는 권한입니다.',
              "validation_error",
            ),
          )
        : Promise.resolve({ ok: true }),
    );
    render(
      <MCPOAuthSettings
        settings={configured()}
        notify={() => {}}
        reload={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /MCP SSO 설정 저장/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("mcp:read");
    expect(written("mcp.oauth.enabled")).toBeUndefined();
  });

  it("warns when the OIDC issuer is missing, because the switch is refused then", () => {
    const settings = configured().map((s) =>
      s.key === "oidc" ? row("oidc", { enabled: false, issuer: "" }) : s,
    );
    render(
      <MCPOAuthSettings
        settings={settings}
        notify={() => {}}
        reload={() => {}}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Issuer URL");
  });
});

describe("mcpResource", () => {
  it("prefers what was written, then the public address, then this origin", () => {
    expect(
      mcpResource(
        "https://mcp.example/mcp",
        "https://vendra.example",
        "http://localhost",
      ),
    ).toBe("https://mcp.example/mcp");
    expect(
      mcpResource("  ", "https://vendra.example/", "http://localhost"),
    ).toBe("https://vendra.example/mcp");
    expect(mcpResource("", "", "http://localhost:5173")).toBe(
      "http://localhost:5173/mcp",
    );
    expect(mcpMetadataUrl("https://mcp.example/mcp")).toBe(
      "https://mcp.example/.well-known/oauth-protected-resource/mcp",
    );
  });
});
