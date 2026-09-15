export type FileCategory = "text" | "image" | "binary";
export type FilePreviewKind = "markdown" | "html" | "none";
export type FileViewerKind = "text" | "image" | "binary";

const IMAGE_EXTENSIONS = new Set([
  "png",
  "jpg",
  "jpeg",
  "gif",
  "svg",
  "webp",
  "ico",
  "bmp",
  "avif",
]);

/**
 * Image formats whose bytes are valid UTF-8 text. The backend judges binary by
 * UTF-8 validity, so these never set `is_binary` and must be recognised from
 * the extension alone.
 */
const TEXT_IMAGE_EXTENSIONS = new Set(["svg"]);

const BINARY_EXTENSIONS = new Set([
  // Executables & libraries
  "exe",
  "dll",
  "so",
  "dylib",
  "o",
  "a",
  // Archives
  "zip",
  "tar",
  "gz",
  "bz2",
  "7z",
  "rar",
  "xz",
  "zst",
  // Fonts
  "woff",
  "woff2",
  "ttf",
  "otf",
  "eot",
  // Media
  "mp3",
  "mp4",
  "wav",
  "ogg",
  "flac",
  "avi",
  "mov",
  "mkv",
  "webm",
  // Documents
  "pdf",
  "doc",
  "docx",
  "xls",
  "xlsx",
  "ppt",
  "pptx",
  // Compiled / data
  "pyc",
  "pyo",
  "class",
  "wasm",
  "sqlite",
  "db",
  // Images already covered above, but some binary-only image formats
  "psd",
  "tiff",
  "tif",
]);

const MIME_MAP: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  svg: "image/svg+xml",
  webp: "image/webp",
  ico: "image/x-icon",
  bmp: "image/bmp",
  avif: "image/avif",
};

function getExtension(path: string): string {
  const dot = path.lastIndexOf(".");
  if (dot === -1) return "";
  return path.slice(dot + 1).toLowerCase();
}

/**
 * Determine the file category based on extension.
 * Note: the backend's `is_binary` flag is the source of truth for encoding;
 * this only determines which viewer to use.
 */
export function getFileCategory(path: string): FileCategory {
  const ext = getExtension(path);
  if (IMAGE_EXTENSIONS.has(ext)) return "image";
  if (BINARY_EXTENSIONS.has(ext)) return "binary";
  return "text";
}

export function isMarkdownFile(path: string): boolean {
  return getFilePreviewKind(path) === "markdown";
}

/** True for image formats stored as UTF-8 text, which `is_binary` never flags. */
export function isTextEncodedImage(path: string): boolean {
  return TEXT_IMAGE_EXTENSIONS.has(getExtension(path));
}

/**
 * Pick the viewer for a file. Image extensions win over the binary/text split
 * only when the content really is an image: either the backend flagged it as
 * binary, or the extension is a text-encoded image format. A text file that
 * merely carries an image extension (`.png`) stays in the source editor.
 */
export function resolveFileViewerKind(path: string, isBinary: boolean): FileViewerKind {
  const isImageExtension = getFileCategory(path) === "image";
  if (isImageExtension && (isBinary || isTextEncodedImage(path))) return "image";
  if (isBinary) return "binary";
  return "text";
}

export function getFilePreviewKind(path: string, isBinary = false): FilePreviewKind {
  if (isBinary) return "none";
  const ext = getExtension(path);
  if (ext === "md" || ext === "mdx") return "markdown";
  if (ext === "html" || ext === "htm") return "html";
  return "none";
}

/** Return the MIME type for a known image extension, or a fallback. */
export function getImageMimeType(path: string): string {
  const ext = getExtension(path);
  return MIME_MAP[ext] || "application/octet-stream";
}

// `String.fromCharCode(...bytes)` overflows the call stack on large inputs, so
// the byte array is converted in chunks. 0x8000 stays well under the argument
// limit while keeping the number of concatenations small.
const BASE64_CHUNK_SIZE = 0x8000;

/** Base64-encode UTF-8 text, matching the encoding the backend sends for binaries. */
export function utf8ToBase64(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += BASE64_CHUNK_SIZE) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + BASE64_CHUNK_SIZE));
  }
  return btoa(binary);
}

/**
 * Build the `data:` URL for an image viewer.
 *
 * Must stay a `data:` URL: the desktop shell's CSP allows only `'self'` and
 * `data:` for `img-src`, so `blob:` sources are blocked.
 */
export function buildImageSrc(path: string, content: string, isBinary: boolean): string {
  const payload = isBinary ? content : utf8ToBase64(content);
  return `data:${getImageMimeType(path)};base64,${payload}`;
}
