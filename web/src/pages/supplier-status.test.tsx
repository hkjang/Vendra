import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SupplierEdit } from "./Suppliers";
import { supplierStatuses } from "../status";
import { patch } from "../api";
import { Supplier } from "../types";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  patch: vi.fn(),
}));

const save = vi.mocked(patch);

function supplier(status: string): Supplier {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    supplierNumber: "SUP-0001",
    name: "한빛정밀",
    businessNumber: "123-45-67890",
    status,
    riskLevel: "LOW",
    categories: [],
    addresses: [],
    financials: {},
    taxInfo: {},
    metadata: {},
    annualSpend: 0,
    createdAt: "2026-01-01T00:00:00+09:00",
    updatedAt: "2026-01-01T00:00:00+09:00",
  } as Supplier;
}

describe("the 거래 상태 dropdown", () => {
  beforeEach(() => save.mockReset().mockResolvedValue({}));

  // The register's filter, the label map and this dropdown each used to write
  // the status list out for themselves, and the dropdown's copy said
  // "registered" where the other two said "registration". A supplier that had
  // signed itself up through the portal carries "registration", which was not
  // among the options — and a select whose value is not one of its options
  // shows the first one instead. So opening such a supplier and pressing save,
  // with no intention of touching its status, filed it back as 후보: out of the
  // 등록 queue nobody was watching, and into a list nobody works from.
  it("saves back the status the supplier arrived with", async () => {
    render(
      <SupplierEdit
        supplier={supplier("registration")}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(screen.getByLabelText("거래 상태")).toHaveValue("registration");
    await userEvent.click(screen.getByRole("button", { name: "Master 저장" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({ status: "registration" });
  });

  // Choosing 등록 used to store "registered", a spelling nothing else in the
  // application knows: the 등록 filter never returned the supplier, the badge
  // had no Korean label to show and fell back to the raw word, and the API
  // now refuses it outright.
  it("offers every status the rest of the application reads", () => {
    render(
      <SupplierEdit
        supplier={supplier("candidate")}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    const options = Array.from(
      screen.getByLabelText("거래 상태").querySelectorAll("option"),
    );
    expect(options.map((o) => o.value)).toEqual(
      supplierStatuses.map((s) => s.value),
    );
    expect(options.map((o) => o.textContent)).toEqual(
      supplierStatuses.map((s) => s.label),
    );
  });

  it("keeps a status the person actually picked", async () => {
    render(
      <SupplierEdit
        supplier={supplier("screening")}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await userEvent.selectOptions(screen.getByLabelText("거래 상태"), "approved");
    await userEvent.click(screen.getByRole("button", { name: "Master 저장" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({ status: "approved" });
  });
});
