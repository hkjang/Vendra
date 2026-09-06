import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import Portal from "./Portal";
import { ToastProvider } from "../feedback";
import { api } from "../api";
import type { Principal, Version } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
}));

const call = vi.mocked(api);

const user: Principal = {
  id: "u1",
  email: "bidder@vendor.test",
  displayName: "포털 담당자",
  userType: "supplier",
  supplierId: "s1",
  permissions: ["portal.*"],
  dataScope: "own",
};
const version: Version = {
  name: "Vendra",
  version: "test",
  commit: "test",
  buildTime: "test",
};

function tender(status: string, responseStatus?: string) {
  return {
    id: "t1",
    objectType: "rfq",
    number: "RFQ-AWARDED-1",
    title: "낙찰이 끝난 견적요청",
    status,
    dueDate: "2026-12-31",
    data: {},
    response: responseStatus
      ? { id: "r1", status: responseStatus, totalAmount: 3000000 }
      : undefined,
  };
}

function show(item: ReturnType<typeof tender>) {
  call.mockImplementation(async (path: string) => {
    if (path.includes("/portal/profile"))
      return { supplier: { id: "s1", name: "응찰 업체" }, user } as never;
    if (path.includes("/portal/sourcing")) return { items: [item] } as never;
    return { items: [] } as never;
  });
  window.location.hash = "#rfq";
  return render(
    <MemoryRouter>
      <ToastProvider>
        <Portal user={user} version={version} onLogout={() => {}} />
      </ToastProvider>
    </MemoryRouter>,
  );
}

/**
 * The card showed the bid's own status, in the word the database stores it in.
 *
 * A company the committee had passed over therefore read 제출 완료 — its own
 * last action, in the same blue as everyone else's — with the 응답 확인 · 수정
 * button still offered, and pressing save through it used to wipe the 미선정 the
 * award had written. Nothing on the page said the decision had been made.
 */
describe("the portal card carries the award", () => {
  it("names the decision instead of the bid it replaced", async () => {
    show(tender("not_selected", "submitted"));
    expect(await screen.findByText("미선정")).toBeInTheDocument();
    expect(screen.queryByText("submitted")).not.toBeInTheDocument();
    expect(screen.queryByText("제출 완료")).not.toBeInTheDocument();
  });

  it("stops offering the response form once the award is final", async () => {
    show(tender("not_selected", "submitted"));
    const edit = await screen.findByRole("button", { name: /응답 확인/ });
    expect(edit).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "참여 거절" }),
    ).not.toBeInTheDocument();
  });

  /**
   * 우선협상 is the one selection that expects the quote to keep moving, so the
   * card says so and the form stays open.
   */
  it("keeps the form open for the preferred bidder", async () => {
    show(tender("preferred", "submitted"));
    expect(await screen.findByText("우선협상")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /응답 확인/ })).toBeEnabled(),
    );
  });

  /**
   * Every other standing still reports the bid, in Korean rather than as the
   * database spells it.
   */
  it("labels a bid that is still in the running", async () => {
    show(tender("submitted", "draft"));
    expect(await screen.findByText("작성 중")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /응답 확인/ }),
    ).toBeInTheDocument();
  });
});
