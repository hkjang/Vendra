import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SearchPage } from "./Objects";
import { api } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
}));

const search = vi.mocked(api);

function renderSearch(query: string) {
  return render(
    <MemoryRouter initialEntries={[`/search?q=${encodeURIComponent(query)}`]}>
      <SearchPage />
    </MemoryRouter>,
  );
}

const row = (title: string) => ({
  id: title,
  type: "supplier",
  number: "SUP-1",
  title,
  status: "active",
});

describe("SearchPage", () => {
  beforeEach(() => search.mockReset());

  it("says results were cut off, naming what was cut", async () => {
    // Each leg of the search is capped. A cap the answer does not mention
    // reads as "this is everything": in a register full of similarly named
    // companies, someone not seeing theirs among ten concludes it is not
    // registered.
    search.mockResolvedValue({
      items: Array.from({ length: 10 }, (_, i) => row(`정밀 ${i}`)),
      truncatedCategories: ["공급업체"],
    });
    renderSearch("정밀");
    expect(await screen.findByText("정밀 0")).toBeInTheDocument();
    const notice = screen.getByRole("status");
    expect(notice).toHaveTextContent("공급업체");
    expect(notice).toHaveTextContent("검색어를 좁히면");
  });

  it("stays quiet when everything fits", async () => {
    search.mockResolvedValue({
      items: [row("한빛정밀")],
      truncatedCategories: [],
    });
    renderSearch("한빛정밀");
    expect(await screen.findByText("한빛정밀")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("stays quiet when the field is absent, so an older server does not lie", async () => {
    search.mockResolvedValue({ items: [row("한빛정밀")] });
    renderSearch("한빛정밀");
    expect(await screen.findByText("한빛정밀")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("does not claim nothing was found when it never looked", async () => {
    // One character never reaches the server. Answering "검색 결과가 없습니다"
    // tells someone their supplier is not registered when nothing was looked
    // for — and a supplier name is exactly what people type one letter of.
    renderSearch("한");
    await waitFor(() => expect(search).not.toHaveBeenCalled());
    expect(screen.getByText("한 글자 더 입력하세요")).toBeInTheDocument();
    expect(screen.queryByText("검색 결과가 없습니다")).not.toBeInTheDocument();
  });

  it("says nothing was found only after looking", async () => {
    search.mockResolvedValue({ items: [], truncatedCategories: [] });
    renderSearch("없는업체");
    expect(await screen.findByText("검색 결과가 없습니다")).toBeInTheDocument();
  });

  it("invites a search before anything is typed", () => {
    renderSearch("");
    expect(screen.getByText("무엇을 찾고 계세요?")).toBeInTheDocument();
    expect(search).not.toHaveBeenCalled();
  });
});
