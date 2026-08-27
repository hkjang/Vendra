import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import Portal from "./Portal";
import { ToastProvider } from "../feedback";
import { api, APIError } from "../api";
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

const tender = {
  id: "t1",
  objectType: "rfq",
  number: "RFQ-OPEN-1",
  title: "마감이 오늘인 견적요청",
  status: "invited",
  dueDate: "2026-12-31",
  data: {},
};

function show(hash: string) {
  window.location.hash = hash;
  return render(
    <MemoryRouter>
      <ToastProvider>
        <Portal user={user} version={version} onLogout={() => {}} />
      </ToastProvider>
    </MemoryRouter>,
  );
}

/**
 * The portal used to fetch its four endpoints through a single Promise.all, so
 * whichever failed took the rest with it: a supplier with a bid closing today
 * lost the tender list because their evaluation history would not load, and saw
 * nothing but the loading screen.
 */
describe("Portal sections load independently", () => {
  const route = (failing: string) =>
    call.mockImplementation(async (path: string) => {
      if (path.includes(failing))
        throw new APIError(503, "조회에 실패했습니다");
      if (path.includes("/portal/profile"))
        return { supplier: { id: "s1", name: "응찰 업체" }, user } as never;
      if (path.includes("/portal/sourcing"))
        return { items: [tender] } as never;
      return { items: [] } as never;
    });

  it("still shows an open tender when another section fails", async () => {
    route("/portal/evaluations");
    show("#rfq");
    await waitFor(() =>
      expect(document.body.textContent).toContain("마감이 오늘인 견적요청"),
    );
  });

  it("says a failed section failed rather than showing it as empty", async () => {
    route("/portal/evaluations");
    show("#evaluation");
    await waitFor(() =>
      expect(document.body.textContent).toContain(
        "평가 결과를 불러오지 못했습니다",
      ),
    );
    expect(document.body.textContent).not.toContain(
      "공개된 평가 결과가 없습니다",
    );
    expect(screen.getByRole("button", { name: /다시 시도/ })).toBeTruthy();
  });
});
