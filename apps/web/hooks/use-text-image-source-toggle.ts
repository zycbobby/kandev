"use client";

import { useCallback, useMemo, useState } from "react";
import { isTextEncodedImage } from "@/lib/utils/file-types";

/**
 * Session-local "show the SVG source instead of the rendered image" state.
 *
 * Not persisted by design: reopening a file always starts on the image. State
 * is keyed to `fileIdentity` so a reused viewer host (a dockview panel that
 * outlives the file) resets rather than inheriting the previous file's mode.
 */
export function useTextImageSourceToggle(
  fileIdentity: string,
  isBinary: boolean,
  path: string,
): {
  enabled: boolean;
  showSource: boolean;
  showSourceCode: () => void;
  showImage: () => void;
} {
  const enabled = !isBinary && isTextEncodedImage(path);
  const [showSource, setShowSource] = useState(false);
  const [lastFileIdentity, setLastFileIdentity] = useState(fileIdentity);

  // Adjust state during render (React's recommended alternative to an effect)
  // so the reset is applied before the toggled-away view can paint.
  if (lastFileIdentity !== fileIdentity) {
    setLastFileIdentity(fileIdentity);
    setShowSource(false);
  }

  const showSourceCode = useCallback(() => setShowSource(true), []);
  const showImage = useCallback(() => setShowSource(false), []);

  return useMemo(
    () => ({ enabled, showSource: enabled && showSource, showSourceCode, showImage }),
    [enabled, showSource, showSourceCode, showImage],
  );
}
