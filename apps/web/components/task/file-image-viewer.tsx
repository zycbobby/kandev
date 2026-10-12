"use client";

import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { IconCode } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { buildImageSrc } from "@/lib/utils/file-types";
import { FileViewerHeader } from "./file-viewer-header";

type FileImageViewerProps = {
  path: string;
  isSymlink?: boolean;
  content: string; // base64-encoded when binary, UTF-8 text otherwise
  isBinary?: boolean;
  worktreePath?: string;
  headerActions?: ReactNode;
  onShowSource?: () => void;
};

export function FileImageViewer({
  path,
  isSymlink,
  content,
  isBinary = false,
  worktreePath,
  headerActions,
  onShowSource,
}: FileImageViewerProps) {
  const { t } = useTranslation();
  const src = buildImageSrc(path, content, isBinary);

  return (
    <div className="flex flex-col h-full">
      <FileViewerHeader
        path={path}
        isSymlink={isSymlink}
        worktreePath={worktreePath}
        actions={
          <>
            {onShowSource && (
              <Button
                size="sm"
                variant="ghost"
                onClick={onShowSource}
                className="h-11 cursor-pointer gap-1.5 px-2.5 sm:h-8"
                data-testid="show-source-toggle"
              >
                <IconCode className="h-4 w-4" />
                {t("task:showCode")}
              </Button>
            )}
            {headerActions}
          </>
        }
      />
      <div className="flex-1 flex items-center justify-center overflow-auto p-6">
        <img
          src={src}
          alt={path}
          data-testid="image-viewer"
          className="max-w-full max-h-full object-contain rounded"
          draggable={false}
        />
      </div>
    </div>
  );
}
