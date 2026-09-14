import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MailSettings } from "./Admin";
import { APIError, api, post, put } from "../api";

// The wrappers close over the real api, so each one the panel writes through
// is mocked by name or a form submit silently bypasses the mock.
vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
}));

const call = vi.mocked(api);
const save = vi.mocked(put);
const send = vi.mocked(post);

const row = (
  key: string,
  value: unknown,
  extra: Partial<{ secret: boolean; secretConfigured: boolean }> = {},
) => ({
  key,
  value,
  secret: false,
  secretConfigured: false,
  category: "mail",
  updatedAt: "2026-09-14T03:00:00Z",
  ...extra,
});

// What the migration installs: off, port 25, no password.
const installed = () => [
  row("mail.enabled", false),
  row("mail.smtp_host", ""),
  row("mail.smtp_port", 25),
  row("mail.security", "auto"),
  row("mail.skip_tls_verify", false),
  row("mail.username", ""),
  row("mail.password", "", { secret: true }),
  row("mail.from_address", ""),
  row("mail.from_name", "Vendra"),
  row("mail.base_url", ""),
  row("mail.timeout_seconds", 10),
  row("mail.notify_approval_request", true),
  row("mail.notify_approval_decision", true),
  row("mail.notify_expiry", true),
  row("mail.notify_alert", true),
];

function answer(deliveries: unknown[] = []) {
  call.mockImplementation((path: string) => {
    if (path.startsWith("/api/v1/admin/mail/deliveries")) {
      return Promise.resolve({
        items: deliveries,
        total: deliveries.length,
        status: { sent: deliveries.length },
        truncated: false,
      }) as ReturnType<typeof api>;
    }
    return Promise.resolve({}) as ReturnType<typeof api>;
  });
  save.mockResolvedValue({ ok: true });
  send.mockResolvedValue({ sent: true, recipient: "ops@corp.example" });
}

const saved = () =>
  Object.fromEntries(
    save.mock.calls.map(([path, body]) => [
      String(path).replace("/api/v1/admin/settings/", ""),
      body as Record<string, unknown>,
    ]),
  );

describe("MailSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows a fresh installation as off and writes only what changed, under the standard names", async () => {
    answer();
    const notify = vi.fn();
    render(
      <MailSettings settings={installed()} notify={notify} reload={vi.fn()} />,
    );
    expect(screen.getByText("비활성")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("비밀번호 입력")).toBeInTheDocument();
    await screen.findByText("발송 기록이 없습니다");

    fireEvent.click(screen.getByLabelText(/메일 알림 사용/));
    fireEvent.change(screen.getByPlaceholderText("relay.internal"), {
      target: { value: " relay.corp.example " },
    });
    fireEvent.change(screen.getByPlaceholderText("vendra@corp.example"), {
      target: { value: "vendra@corp.example" },
    });
    fireEvent.click(screen.getByLabelText(/SLA 위반 · 계약금액 초과/));
    fireEvent.click(screen.getByText("메일 알림 설정 저장"));

    await waitFor(() => expect(notify).toHaveBeenCalled());
    const written = saved();
    // Four rows changed; the other eleven — and the untouched password —
    // are not written at all.
    expect(Object.keys(written).sort()).toEqual([
      "mail.enabled",
      "mail.from_address",
      "mail.notify_alert",
      "mail.smtp_host",
    ]);
    expect(written["mail.enabled"]).toEqual({ value: true, category: "mail" });
    expect(written["mail.smtp_host"]).toEqual({
      value: "relay.corp.example",
      category: "mail",
    });
    expect(written["mail.notify_alert"].value).toBe(false);
  });

  it("sends a typed password as the secret and never as a value, and shows a stored one only as configured", async () => {
    answer();
    const settings = installed().map((s) =>
      s.key === "mail.password" ? { ...s, secretConfigured: true } : s,
    );
    render(
      <MailSettings settings={settings} notify={vi.fn()} reload={vi.fn()} />,
    );
    const password = screen.getByPlaceholderText("설정됨 · 변경하려면 입력");
    fireEvent.change(password, { target: { value: "relay-pw" } });
    fireEvent.click(screen.getByText("메일 알림 설정 저장"));
    await waitFor(() =>
      expect(saved()["mail.password"]).toEqual({
        value: "",
        category: "mail",
        secret: true,
        secretValue: "relay-pw",
      }),
    );
  });

  it("shows the server's refusal in place instead of throwing past the form", async () => {
    answer();
    save.mockRejectedValueOnce(
      new APIError(
        400,
        "mail.smtp_port 는 1~65535 사이의 정수여야 합니다",
        "validation_error",
      ),
    );
    render(
      <MailSettings settings={installed()} notify={vi.fn()} reload={vi.fn()} />,
    );
    fireEvent.change(screen.getByPlaceholderText("relay.internal"), {
      target: { value: "relay.corp.example" },
    });
    fireEvent.click(screen.getByText("메일 알림 설정 저장"));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "mail.smtp_port 는 1~65535 사이의 정수여야 합니다",
    );
  });

  it("proves the relay with a test send and shows the answer either way", async () => {
    answer();
    render(
      <MailSettings settings={installed()} notify={vi.fn()} reload={vi.fn()} />,
    );
    fireEvent.change(screen.getByPlaceholderText("ops@corp.example"), {
      target: { value: "ops@corp.example" },
    });
    fireEvent.click(screen.getByText("시험 메일 보내기"));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "ops@corp.example 으로 시험 메일을 보냈습니다",
    );
    expect(send).toHaveBeenCalledWith("/api/v1/admin/mail/test", {
      recipient: "ops@corp.example",
    });
    // The log is re-read after a send so the new row shows.
    expect(
      call.mock.calls.filter(([p]) =>
        String(p).startsWith("/api/v1/admin/mail/deliveries"),
      ).length,
    ).toBeGreaterThanOrEqual(2);

    send.mockRejectedValueOnce(
      new APIError(
        502,
        "SMTP 연결 실패: connection refused",
        "mail_send_failed",
      ),
    );
    fireEvent.click(screen.getByText("시험 메일 보내기"));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "SMTP 연결 실패: connection refused",
    );
  });

  it("lists what went out with its outcome and never a body", async () => {
    answer([
      {
        id: "d1",
        event: "approval.requested",
        recipient: "kim@corp.example",
        subject: "[Vendra] 결재 요청: 지급 서버 12대",
        status: "sent",
        attempts: 1,
        createdAt: "2026-09-14T03:00:00Z",
        updatedAt: "2026-09-14T03:00:01Z",
      },
      {
        id: "d2",
        event: "test",
        recipient: "ops@corp.example",
        subject: "[Vendra] SMTP 시험 발송",
        status: "failed",
        attempts: 1,
        errorMessage: "SMTP 연결 실패: connection refused",
        createdAt: "2026-09-14T03:05:00Z",
        updatedAt: "2026-09-14T03:05:01Z",
      },
    ]);
    render(
      <MailSettings settings={installed()} notify={vi.fn()} reload={vi.fn()} />,
    );
    expect(await screen.findByText("발송됨")).toBeInTheDocument();
    expect(screen.getByText("실패")).toBeInTheDocument();
    expect(
      screen.getByText("SMTP 연결 실패: connection refused"),
    ).toBeInTheDocument();
    expect(screen.getByText("kim@corp.example")).toBeInTheDocument();
    expect(screen.getByText(/전체 2건 · 성공 2/)).toBeInTheDocument();
  });
});
