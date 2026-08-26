import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BootSplash, Loading, Toast } from "./components";

describe("BootSplash", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("says nothing while the wait is still ordinary", () => {
    render(<BootSplash stalledAfterMs={8000} />);
    act(() => void vi.advanceTimersByTime(7999));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("explains itself once the wait stops looking ordinary", () => {
    // A logo and a bar, indefinitely, gives no way to tell a slow start from a
    // dead one.
    render(<BootSplash stalledAfterMs={8000} />);
    act(() => void vi.advanceTimersByTime(8000));
    const notice = screen.getByRole("status");
    expect(notice).toHaveTextContent("오래 걸립니다");
    expect(screen.getByRole("button", { name: /다시 시도/ })).toBeInTheDocument();
  });

  it("drops its timer when it goes away, so a finished boot stays quiet", () => {
    const { unmount } = render(<BootSplash stalledAfterMs={8000} />);
    unmount();
    act(() => void vi.advanceTimersByTime(20000));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});

describe("Loading", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("waits longer than the boot splash before saying anything", () => {
    render(<Loading stalledAfterMs={12000} />);
    act(() => void vi.advanceTimersByTime(11999));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    act(() => void vi.advanceTimersByTime(1));
    expect(screen.getByRole("status")).toBeInTheDocument();
  });
});

describe("Toast", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("closes itself once, after its duration", () => {
    const onClose = vi.fn();
    render(<Toast message="저장했습니다" duration={4500} onClose={onClose} />);
    expect(screen.getByText("저장했습니다")).toBeInTheDocument();
    act(() => void vi.advanceTimersByTime(4499));
    expect(onClose).not.toHaveBeenCalled();
    act(() => void vi.advanceTimersByTime(1));
    expect(onClose).toHaveBeenCalledTimes(1);
    act(() => void vi.advanceTimersByTime(20000));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
