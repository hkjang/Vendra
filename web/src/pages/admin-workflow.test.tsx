import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { WorkflowPanel } from "./Admin";
import { api, post } from "../api";
import { workflowObjectTypes } from "../status";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  post: vi.fn(),
}));

const call = vi.mocked(api);
const send = vi.mocked(post);

/**
 * The 업무 유형 dropdown is the only place an approval rule is created from, and
 * it wrote out a list of four types of its own. 공급업체 saved "supplier", which
 * nothing in the application ever submits — the rule was accepted, listed as
 * 활성 and matched nothing. The eight real types it omitted are the other half
 * of the same defect: 납품, 검수, 품질, 이슈, RFQ, RFP, Invoice and 지급 all
 * carry a 승인 요청 button, and no rule could be put in front of any of them, so
 * an invoice sent for approval came back 승인 with no approver behind it.
 */
describe("승인 Workflow 만들기", () => {
  function mount() {
    call.mockImplementation((path: string) => {
      if (path === "/api/v1/workflows") {
        return Promise.resolve({
          items: [
            {
              id: "w1",
              name: "공급업체 계좌정보 변경 승인",
              objectType: "supplier_bank_change",
              enabled: true,
              conditions: {},
              steps: [{ name: "구매 관리자 승인", role: "procurement_manager" }],
              version: 1,
              updatedAt: "2026-09-06T00:00:00Z",
            },
          ],
        }) as ReturnType<typeof api>;
      }
      return Promise.resolve({
        items: [
          {
            key: "workflow.approval_enabled",
            value: true,
            secret: false,
            secretConfigured: false,
            category: "workflow",
            updatedAt: "2026-09-06T00:00:00Z",
          },
        ],
      }) as ReturnType<typeof api>;
    });
    send.mockResolvedValue({} as never);
    return render(<WorkflowPanel notify={() => {}} />);
  }

  it("offers every type something submits, and nothing else", async () => {
    mount();
    fireEvent.click(await screen.findByText("Workflow"));

    const select = (await screen.findByLabelText(
      "업무 유형",
    )) as HTMLSelectElement;
    expect([...select.options].map((o) => o.value)).toEqual(
      workflowObjectTypes.map((t) => t.value),
    );
    // The option that produced a rule nothing could ever match.
    expect([...select.options].map((o) => o.value)).not.toContain("supplier");
    // And the eight the form used to leave out, of which 지급 is one.
    expect([...select.options].map((o) => o.value)).toContain("payment");

    // The condition is matched against the submitted 업무 객체's own grade, not
    // the supplier's; the label used to send whoever read it to the wrong
    // record.
    expect(screen.getByLabelText("업무 Risk 조건")).toBeTruthy();
    expect(screen.queryByLabelText("공급업체 Risk 조건")).toBeNull();
  });

  it("files the rule under the type the buyer chose", async () => {
    mount();
    fireEvent.click(await screen.findByText("Workflow"));

    fireEvent.change(await screen.findByLabelText(/Workflow 이름/), {
      target: { value: "지급 승인" },
    });
    fireEvent.change(screen.getByLabelText("업무 유형"), {
      target: { value: "payment" },
    });
    fireEvent.submit(screen.getByLabelText(/Workflow 이름/).closest("form")!);

    await waitFor(() => expect(send).toHaveBeenCalled());
    expect(send.mock.calls[0][0]).toBe("/api/v1/workflows");
    expect((send.mock.calls[0][1] as { objectType: string }).objectType).toBe(
      "payment",
    );
  });

  it("says the type in the words the form named it by", async () => {
    mount();
    // It printed the stored name, so the rule installed with the schema read
    // "supplier_bank_change" in the middle of the Workflow list.
    expect(
      await screen.findByText("공급업체 계좌정보 변경 · v1"),
    ).toBeTruthy();
  });
});
