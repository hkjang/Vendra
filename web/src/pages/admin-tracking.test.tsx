import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TrackingSettings } from "./Admin";
import { APIError, api, del, post, put } from "../api";

// The wrappers close over the real api, so each one the panel writes through
// is mocked by name or a form submit silently bypasses the mock.
vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
  del: vi.fn(),
}));

const call = vi.mocked(api);
const save = vi.mocked(put);
const send = vi.mocked(post);
const wipe = vi.mocked(del);

const setting = (value: Record<string, unknown>) => [
  {
    key: "tracking",
    value,
    secret: false,
    secretConfigured: false,
    category: "tracking",
    updatedAt: "2026-09-12T03:00:00Z",
  },
];

const blocked = {
  origin: "https://pixel.tracker.example",
  directive: "img-src",
  page: "/suppliers",
  count: 12,
  lastSeen: "2026-09-12T03:00:00Z",
  allowed: false,
};

function answer(violations: unknown[] = []) {
  call.mockImplementation((path: string) => {
    if (path === "/api/v1/admin/tracking/violations") {
      return Promise.resolve({ items: violations }) as ReturnType<typeof api>;
    }
    return Promise.resolve({}) as ReturnType<typeof api>;
  });
  save.mockResolvedValue({ ok: true });
  send.mockResolvedValue({ ok: true });
  wipe.mockResolvedValue(undefined);
}

const submitted = () =>
  save.mock.calls.find(
    ([path]) => path === "/api/v1/admin/settings/tracking",
  )?.[1];

describe("TrackingSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("offers Momento first and, through the proxy, sends what the server stores", async () => {
    answer();
    render(
      <TrackingSettings
        settings={setting({})}
        notify={() => undefined}
        reload={() => undefined}
      />,
    );
    // A fresh installation is off, on Momento, through the proxy.
    expect(screen.getByText("비활성")).toBeInTheDocument();
    const provider = screen.getByLabelText(/추적 도구/) as HTMLSelectElement;
    expect(provider.value).toBe("momento");
    expect(provider.options[0].value).toBe("momento");
    expect(
      (
        screen.getByRole("checkbox", {
          name: /같은 오리진 프록시/,
        }) as HTMLInputElement
      ).checked,
    ).toBe(true);

    fireEvent.click(screen.getByRole("checkbox", { name: /방문 추적 사용/ }));
    fireEvent.change(screen.getByLabelText(/Momento 수집기 주소/), {
      target: { value: "https://momento.internal" },
    });
    fireEvent.change(screen.getByLabelText(/^사이트 ID/), {
      target: { value: "vendra-prd" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: /방문 추적 설정 저장/ }),
    );

    await waitFor(() => expect(submitted()).toBeTruthy());
    expect(submitted()).toEqual({
      category: "tracking",
      value: expect.objectContaining({
        enabled: true,
        provider: "momento",
        momentoUrl: "https://momento.internal",
        momentoSiteId: "vendra-prd",
        momentoProxy: true,
        includeAdmin: false,
        placement: "head",
      }),
    });
  });

  it("shows the boxes the chosen provider needs and the server's refusal", async () => {
    answer();
    render(
      <TrackingSettings
        settings={setting({ enabled: true, provider: "custom" })}
        notify={() => undefined}
        reload={() => undefined}
      />,
    );
    expect(screen.getByText("사용 중")).toBeInTheDocument();
    const snippet = screen.getByLabelText(/추적 코드/) as HTMLTextAreaElement;
    expect(snippet).toBeInTheDocument();
    expect(screen.queryByLabelText(/Momento 수집기 주소/)).toBeNull();

    // Over the limit the button is disabled before the server is asked.
    fireEvent.change(snippet, { target: { value: "x".repeat(8 * 1024 + 1) } });
    expect(
      screen.getByText(/8,192 바이트를 넘을 수 없습니다/),
    ).toBeInTheDocument();
    expect(
      (
        screen.getByRole("button", {
          name: /방문 추적 설정 저장/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);

    // What the server refuses is shown where it came from, not thrown away.
    fireEvent.change(snippet, { target: { value: "" } });
    save.mockRejectedValue(
      new APIError(400, "customSnippet이 비어 있습니다", "validation_error"),
    );
    fireEvent.click(
      screen.getByRole("button", { name: /방문 추적 설정 저장/ }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "customSnippet이 비어 있습니다",
    );

    // Switching provider swaps the boxes.
    fireEvent.change(screen.getByLabelText(/추적 도구/), {
      target: { value: "ga4" },
    });
    expect(screen.getByLabelText(/측정 ID/)).toBeInTheDocument();
    expect(screen.queryByLabelText(/추적 코드/)).toBeNull();
  });

  it("lists what the policy blocked and allows it in one click", async () => {
    answer([
      blocked,
      { ...blocked, origin: "https://ok.example", allowed: true },
    ]);
    const reload = vi.fn();
    render(
      <TrackingSettings
        settings={setting({ enabled: true, provider: "momento" })}
        notify={() => undefined}
        reload={reload}
      />,
    );
    expect(
      await screen.findByText("https://pixel.tracker.example"),
    ).toBeInTheDocument();
    expect(screen.getByText("허용됨")).toBeInTheDocument();
    const allowButtons = screen.getAllByRole("button", { name: "허용" });
    expect(allowButtons).toHaveLength(1);

    answer([{ ...blocked, allowed: true }]);
    fireEvent.click(allowButtons[0]);
    await waitFor(() =>
      expect(send).toHaveBeenCalledWith(
        "/api/v1/admin/tracking/allowed-hosts",
        {
          origin: "https://pixel.tracker.example",
        },
      ),
    );
    // The settings are reloaded so the allow-list box shows the new entry,
    // and the list is re-read so the row turns into "허용됨".
    await waitFor(() => expect(reload).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "허용" })).toBeNull(),
    );
  });
});
