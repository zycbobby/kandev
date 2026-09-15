import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useTextImageSourceToggle } from "./use-text-image-source-toggle";

const SVG_PATH = "assets/logo.svg";

afterEach(cleanup);

describe("useTextImageSourceToggle", () => {
  it("enables the toggle for a text-encoded image", () => {
    const { result } = renderHook(() =>
      useTextImageSourceToggle("session\u0000repo\u0000assets/logo.svg", false, SVG_PATH),
    );

    expect(result.current.enabled).toBe(true);
    expect(result.current.showSource).toBe(false);
  });

  it("switches to source and back to the image", () => {
    const { result } = renderHook(() =>
      useTextImageSourceToggle("session\u0000repo\u0000assets/logo.svg", false, SVG_PATH),
    );

    act(() => result.current.showSourceCode());
    expect(result.current.showSource).toBe(true);

    act(() => result.current.showImage());
    expect(result.current.showSource).toBe(false);
  });

  it("resets to the image when the file identity changes", () => {
    const { result, rerender } = renderHook(
      ({ identity }: { identity: string }) => useTextImageSourceToggle(identity, false, SVG_PATH),
      { initialProps: { identity: "session\u0000repo\u0000assets/first.svg" } },
    );

    act(() => result.current.showSourceCode());
    expect(result.current.showSource).toBe(true);

    rerender({ identity: "session\u0000repo\u0000assets/second.svg" });
    expect(result.current.showSource).toBe(false);
  });

  it("stays disabled for binary image formats and plain text", () => {
    const { result: binaryImage } = renderHook(() =>
      useTextImageSourceToggle("identity-png", true, "assets/logo.png"),
    );
    expect(binaryImage.current.enabled).toBe(false);

    const { result: plainText } = renderHook(() =>
      useTextImageSourceToggle("identity-ts", false, "src/index.ts"),
    );
    expect(plainText.current.enabled).toBe(false);
  });

  it("keeps showSource false when the toggle is disabled", () => {
    const { result } = renderHook(() =>
      useTextImageSourceToggle("identity-ts", false, "src/index.ts"),
    );

    act(() => result.current.showSourceCode());
    expect(result.current.showSource).toBe(false);
  });
});
