import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SupplierEdit } from "./Suppliers";
import { APIError, patch } from "../api";
import { Supplier } from "../types";

// api as well as patch: the form asks for the 담당자 candidates as it opens,
// and the real one would reach for fetch.
vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn().mockResolvedValue({ items: [] }),
  patch: vi.fn(),
}));

const save = vi.mocked(patch);

function supplier(): Supplier {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    supplierNumber: "SUP-0001",
    name: "한빛정밀",
    businessNumber: "123-45-67891",
    corporateNumber: "110111-0000001",
    status: "active",
    riskLevel: "LOW",
    categories: [],
    addresses: [],
    financials: {},
    taxInfo: {},
    metadata: {},
    tradingSince: "2020-01-02",
    annualSpend: 0,
    createdAt: "2026-01-01T00:00:00+09:00",
    updatedAt: "2026-01-01T00:00:00+09:00",
  } as Supplier;
}

// The registration was the only place these could be typed, and nothing could
// retype them: the edit form had no box for the 사업자번호 and the API dropped
// the field if a client sent it anyway. A wrong digit hid the company from the
// search box that looks records up by that number, and the only way out was to
// delete the supplier — along with every contract, order and evaluation on it.
describe("the 공급업체 Master 편집 registration numbers", () => {
  // Braces on purpose: the chain returns the mock itself, and a beforeEach
  // that returns a function hands Vitest that function as the teardown hook —
  // which then calls the mocked patch once more, after the test, with whatever
  // implementation it was left with.
  beforeEach(() => {
    save.mockReset().mockResolvedValue({});
  });

  it("shows the numbers the supplier is registered under", () => {
    render(
      <SupplierEdit
        supplier={supplier()}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(screen.getByLabelText(/사업자번호/)).toHaveValue("123-45-67891");
    expect(screen.getByLabelText("법인번호")).toHaveValue("110111-0000001");
    expect(screen.getByLabelText("거래 시작일")).toHaveValue("2020-01-02");
  });

  it("sends the corrected 사업자번호", async () => {
    render(
      <SupplierEdit
        supplier={supplier()}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    const box = screen.getByLabelText(/사업자번호/);
    await userEvent.clear(box);
    await userEvent.type(box, "123-45-67890");
    await userEvent.click(screen.getByRole("button", { name: "Master 저장" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({
      businessNumber: "123-45-67890",
      corporateNumber: "110111-0000001",
      tradingSince: "2020-01-02",
    });
  });

  // The number is the register's key, so the correction can collide with a
  // company already on file under it. The API answers 409 rather than a save
  // failure, and the form has to say which number was refused.
  it("shows the refusal when another company holds the number", async () => {
    render(
      <SupplierEdit
        supplier={supplier()}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    save.mockRejectedValue(new APIError(409, "이미 등록된 사업자번호입니다"));
    fireEvent.submit(screen.getByLabelText(/사업자번호/).closest("form")!);
    expect(
      await screen.findByText("이미 등록된 사업자번호입니다"),
    ).toBeInTheDocument();
  });
});
