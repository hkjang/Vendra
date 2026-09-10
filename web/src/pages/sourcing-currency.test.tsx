import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Portal from "./Portal";
import { ToastProvider } from "../feedback";
import { api, put } from "../api";
import type { Principal, Version } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  put: vi.fn(),
}));

const call = vi.mocked(api);
const save = vi.mocked(put);

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

// A request put out in dollars. Every screen used to render every amount as
// won, so this is the case where the mistake is invisible.
const tender = {
  id: "t1",
  objectType: "rfq",
  number: "RFQ-USD-1",
  title: "해외 자재 견적요청",
  status: "invited",
  currency: "USD",
  dueDate: "2026-12-31",
  data: {},
  response: {
    id: "r1",
    status: "draft",
    currency: "USD",
    totalAmount: 50000,
  },
};

function show() {
  call.mockImplementation(async (path: string) => {
    if (path.includes("/portal/profile"))
      return { supplier: { id: "s1", name: "응찰 업체" }, user } as never;
    if (path.includes("/portal/sourcing")) return { items: [tender] } as never;
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

beforeEach(() => {
  // Braces: Vitest calls whatever beforeEach returns as a teardown hook, and
  // the chained form returns the mock itself.
  save.mockReset().mockResolvedValue({ id: "r1", status: "draft" } as never);
});

/**
 * The quote form offered KRW, USD, EUR and JPY, and nothing downstream ever
 * read the answer.
 *
 * The buyer's comparison scores price as 100*min_amount/total_amount across the
 * submitted bids, with no rate anywhere in the application to bring two
 * currencies onto one scale, so a bid of 50,000 USD against bids of 68,000,000
 * KRW was not a bid in another currency — it was the smallest number, and it
 * took the whole of the price weight and the top of the table. The comparison
 * then rendered it as ₩50,000 beside the others, so nothing on the page said
 * what had happened.
 */
describe("the portal quote form", () => {
  it("states the currency the request is priced in instead of asking", async () => {
    show();
    fireEvent.click(await screen.findByRole("button", { name: /응답 확인/ }));
    const box = await screen.findByDisplayValue("USD");
    expect(box).toHaveAttribute("readonly");
    // The choice that produced the bid nobody could compare.
    expect(
      screen.queryByRole("combobox", { name: "통화" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "KRW" })).not.toBeInTheDocument();
  });

  it("sends the request's currency with the quote", async () => {
    show();
    fireEvent.click(await screen.findByRole("button", { name: /응답 확인/ }));
    fireEvent.change(await screen.findByDisplayValue("50000"), {
      target: { value: "48000" },
    });
    fireEvent.click(screen.getByRole("button", { name: /최종 제출/ }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({
      currency: "USD",
      totalAmount: 48000,
      submit: true,
    });
  });

  it("quotes the bidder their own amount in the currency they wrote it in", async () => {
    show();
    // The card's 제출 금액. It read ₩50,000 for a quote of $50,000.
    const amount = await screen.findByText(/50,000/);
    expect(amount.textContent).not.toContain("₩");
  });
});
