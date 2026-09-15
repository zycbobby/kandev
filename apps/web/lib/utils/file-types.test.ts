import { describe, expect, it } from "vitest";
import {
  buildImageSrc,
  getFilePreviewKind,
  isMarkdownFile,
  isTextEncodedImage,
  resolveFileViewerKind,
  utf8ToBase64,
} from "./file-types";

const NONE = "none";
const PNG_PATH = "assets/logo.png";
const SVG_PATH = "assets/logo.svg";
const TEXT_PATH = "src/index.ts";

describe("getFilePreviewKind", () => {
  it("recognizes Markdown and HTML preview extensions case-insensitively", () => {
    expect(getFilePreviewKind("docs/README.md")).toBe("markdown");
    expect(getFilePreviewKind("docs/README.MDX")).toBe("markdown");
    expect(getFilePreviewKind("examples/index.html")).toBe("html");
    expect(getFilePreviewKind("examples/index.HTM")).toBe("html");
  });

  it("rejects unsupported and binary files", () => {
    expect(getFilePreviewKind(TEXT_PATH)).toBe(NONE);
    expect(getFilePreviewKind("examples/index.html", true)).toBe(NONE);
  });
});

describe("isMarkdownFile", () => {
  it("recognizes Markdown extensions case-insensitively", () => {
    expect(isMarkdownFile("docs/README.md")).toBe(true);
    expect(isMarkdownFile("docs/README.MDX")).toBe(true);
  });

  it("rejects non-Markdown files", () => {
    expect(isMarkdownFile(TEXT_PATH)).toBe(false);
  });
});

describe("isTextEncodedImage", () => {
  it("recognizes SVG case-insensitively", () => {
    expect(isTextEncodedImage(SVG_PATH)).toBe(true);
    expect(isTextEncodedImage("assets/logo.SVG")).toBe(true);
  });

  it("rejects binary image formats and plain text", () => {
    expect(isTextEncodedImage(PNG_PATH)).toBe(false);
    expect(isTextEncodedImage(TEXT_PATH)).toBe(false);
  });
});

describe("resolveFileViewerKind", () => {
  it("routes text SVG to the image viewer", () => {
    expect(resolveFileViewerKind(SVG_PATH, false)).toBe("image");
  });

  it("keeps a text file with a binary image extension in the editor", () => {
    expect(resolveFileViewerKind(PNG_PATH, false)).toBe("text");
  });

  it("routes binary images to the image viewer", () => {
    expect(resolveFileViewerKind(PNG_PATH, true)).toBe("image");
    expect(resolveFileViewerKind("assets/photo.jpeg", true)).toBe("image");
  });

  it("routes other binary files to the binary viewer", () => {
    expect(resolveFileViewerKind("archive.zip", true)).toBe("binary");
    expect(resolveFileViewerKind("archive.zip", false)).toBe("text");
  });

  it("routes plain text to the editor", () => {
    expect(resolveFileViewerKind(TEXT_PATH, false)).toBe("text");
    expect(resolveFileViewerKind("docs/README.md", false)).toBe("text");
  });
});

describe("utf8ToBase64", () => {
  it("round-trips multibyte UTF-8 through the platform decoder", () => {
    const text = "<svg><text>中文与 emoji 🎨</text></svg>";
    const decoded = new TextDecoder().decode(
      Uint8Array.from(atob(utf8ToBase64(text)), (char) => char.charCodeAt(0)),
    );
    expect(decoded).toBe(text);
  });

  it("encodes an empty string", () => {
    expect(utf8ToBase64("")).toBe("");
  });

  it("encodes content larger than a single chunk without overflowing", () => {
    const text = "a".repeat(0x8000 * 2 + 17);
    expect(utf8ToBase64(text)).toBe(btoa(text));
  });
});

describe("buildImageSrc", () => {
  it("encodes text content as base64 for the file's image MIME type", () => {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg"/>';
    expect(buildImageSrc(SVG_PATH, svg, false)).toBe(`data:image/svg+xml;base64,${btoa(svg)}`);
  });

  it("passes binary content through unchanged", () => {
    expect(buildImageSrc(PNG_PATH, "QUJD", true)).toBe("data:image/png;base64,QUJD");
  });
});
