"use client";

import { SymlinkIndicator } from "@/components/shared/symlink-indicator";

import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { IconEye, IconLoader2 } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { PanelBody, PanelHeaderBarSplit, PanelRoot } from "../panel-primitives";
import { FileViewerContent } from "../file-viewer-content";
import { HtmlPreviewContent } from "../html-preview-content";
import { MarkdownPreviewContent } from "../markdown-preview-content";
import { FileImageViewer } from "../file-image-viewer";
import { FileBinaryViewer } from "../file-binary-viewer";
import { FileViewerDownloadButton } from "../file-viewer-header";
import {
  getFilePreviewKind,
  resolveFileViewerKind,
  type FilePreviewKind,
  type FileViewerKind,
} from "@/lib/utils/file-types";
import { triggerFileDownload } from "@/lib/utils/file-download";
import { useAppStore } from "@/components/state-provider";
import type { OpenFileTab } from "@/lib/types/backend";
import { getSessionWorkspacePath } from "@/lib/session-workspace-path";
import {
  useHtmlPreviewPublisher,
  type HtmlPreviewPublishState,
} from "@/hooks/use-html-preview-publisher";
import {
  ExternalVcsFileLink,
  useExternalVcsFileStatus,
} from "@/components/editors/external-vcs-file-link";
import { textImagePreviewAction, type PreviewAction } from "@/components/editors/preview-action";
import { useTextImageSourceToggle } from "@/hooks/use-text-image-source-toggle";
import { useTranslation } from "react-i18next";

type MobileFileViewerPanelProps = {
  file: OpenFileTab;
  sessionId: string | null;
  onClose: () => void;
  initialRenderedPreview?: boolean;
};

function resolveViewerKind(file: OpenFileTab): FileViewerKind {
  return resolveFileViewerKind(file.path, !!file.isBinary);
}

function MobileViewerBody({
  file,
  viewerKind,
  previewKind,
  renderedPreview,
  worktreePath,
  sessionId,
  taskId,
  repositoryId,
  onTogglePreview,
  onRetryHtmlPreview,
  htmlPreview,
  showSource,
  onShowSource,
}: {
  file: OpenFileTab;
  viewerKind: FileViewerKind;
  previewKind: FilePreviewKind;
  renderedPreview: boolean;
  worktreePath?: string;
  sessionId: string | null;
  taskId: string | null;
  repositoryId?: string;
  onTogglePreview: () => void;
  onRetryHtmlPreview: () => void;
  htmlPreview: Pick<HtmlPreviewPublishState, "status" | "url" | "error"> & {
    isPublishing: boolean;
  };
  showSource: boolean;
  onShowSource?: () => void;
}) {
  let viewerContent: ReactNode;
  if (viewerKind === "image" && !showSource) {
    viewerContent = (
      <FileImageViewer
        path={file.path}
        content={file.content}
        isBinary={!!file.isBinary}
        worktreePath={worktreePath}
        onShowSource={onShowSource}
      />
    );
  } else if (viewerKind === "binary") {
    viewerContent = <FileBinaryViewer path={file.path} worktreePath={worktreePath} />;
  } else if (renderedPreview && previewKind === "html") {
    viewerContent = (
      <HtmlPreviewContent
        path={file.path}
        worktreePath={worktreePath}
        sessionId={sessionId ?? undefined}
        taskId={taskId}
        repositoryId={repositoryId}
        repositoryName={file.repo}
        showExternalVcsLink={false}
        previewUrl={htmlPreview.url}
        isLoading={htmlPreview.isPublishing}
        error={htmlPreview.error}
        onRetry={onRetryHtmlPreview}
        onTogglePreview={onTogglePreview}
      />
    );
  } else if (renderedPreview && previewKind === "markdown") {
    viewerContent = (
      <MarkdownPreviewContent
        path={file.path}
        content={file.content}
        worktreePath={worktreePath}
        sessionId={sessionId ?? undefined}
        taskId={taskId}
        repositoryId={repositoryId}
        repositoryName={file.repo}
        enableComments={!!sessionId}
        showExternalVcsLink={false}
        onTogglePreview={onTogglePreview}
      />
    );
  } else {
    viewerContent = (
      <FileViewerContent
        path={file.path}
        repo={file.repo}
        content={file.content}
        sessionId={sessionId ?? undefined}
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="mobile-file-viewer-content">
      {viewerContent}
    </div>
  );
}

type MobileFileViewerHeaderProps = {
  file: OpenFileTab;
  sessionId: string | null;
  activeTaskId: string | null;
  repositoryId?: string;
  fileStatus: ReturnType<typeof useExternalVcsFileStatus>;
  viewerKind: FileViewerKind;
  previewKind: FilePreviewKind;
  renderedPreview: boolean;
  htmlPreview: { isPublishing: boolean };
  sourcePreviewAction?: PreviewAction;
  onDownload?: () => void;
  onPreviewHtml: () => void;
  onPreviewMarkdown: () => void;
  onClose: () => void;
};

function MobileFileViewerHeader({
  file,
  sessionId,
  activeTaskId,
  repositoryId,
  fileStatus,
  viewerKind,
  previewKind,
  renderedPreview,
  htmlPreview,
  sourcePreviewAction,
  onDownload,
  onPreviewHtml,
  onPreviewMarkdown,
  onClose,
}: MobileFileViewerHeaderProps) {
  const { t } = useTranslation();
  const canPreview = viewerKind === "text" && previewKind !== "none" && !renderedPreview;
  const isHtml = previewKind === "html";
  const handlePreview = isHtml ? onPreviewHtml : onPreviewMarkdown;

  return (
    <PanelHeaderBarSplit
      className="px-2"
      left={
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-xs">{file.path}</span>
          <SymlinkIndicator isSymlink={!!file.resolvedPath} showLabel />
        </div>
      }
      right={
        <div className="flex items-center gap-1">
          <ExternalVcsFileLink
            filePath={file.path}
            previousPath={fileStatus?.old_path}
            status={fileStatus?.status}
            taskId={activeTaskId}
            sessionId={sessionId}
            repositoryId={file.repo ? undefined : repositoryId}
            repositoryName={file.repo}
            size="touch"
          />
          <FileViewerDownloadButton onDownload={onDownload} />
          {sourcePreviewAction && (
            <Button
              variant="ghost"
              size="sm"
              className="h-11 w-11 cursor-pointer p-0"
              onClick={sourcePreviewAction.onClick}
              data-testid={sourcePreviewAction.testId}
              aria-label={sourcePreviewAction.label}
            >
              <IconEye className="h-4 w-4" />
            </Button>
          )}
          {canPreview && (
            <Button
              variant="ghost"
              size="sm"
              className="h-11 w-11 cursor-pointer p-0"
              onClick={handlePreview}
              disabled={isHtml && htmlPreview.isPublishing}
              data-testid={isHtml ? "html-preview-toggle" : "markdown-preview-toggle"}
              aria-label={isHtml ? t("task:openHtmlPreview") : t("task:openMarkdownPreview")}
              title={isHtml ? t("task:htmlPreviewTrustedCode") : undefined}
            >
              {isHtml && htmlPreview.isPublishing ? (
                <IconLoader2 className="h-4 w-4 animate-spin" />
              ) : (
                <IconEye className="h-4 w-4" />
              )}
            </Button>
          )}
          <Button
            variant="ghost"
            size="sm"
            className="min-h-11 min-w-11 cursor-pointer px-2"
            onClick={onClose}
          >
            {t("task:close")}
          </Button>
        </div>
      }
    />
  );
}

export function MobileFileViewerPanel({
  file,
  sessionId,
  onClose,
  initialRenderedPreview = false,
}: MobileFileViewerPanelProps) {
  const activeSession = useAppStore((state) =>
    sessionId ? (state.taskSessions.items[sessionId] ?? null) : null,
  );
  const activeTaskId = useAppStore((state) => state.tasks.activeTaskId);
  const { t } = useTranslation();
  const worktreePath = getSessionWorkspacePath(activeSession);
  const repositoryId = activeSession?.repository_id ?? undefined;
  const fileStatus = useExternalVcsFileStatus(file.path, sessionId, file.repo);
  const viewerKind = useMemo(() => resolveViewerKind(file), [file]);
  const previewKind = getFilePreviewKind(file.path, !!file.isBinary);
  const htmlPreview = useHtmlPreviewPublisher(sessionId);
  const onDownload = useMemo(
    () => () =>
      triggerFileDownload({
        fileName: file.path,
        content: file.content,
        isBinary: !!file.isBinary,
      }),
    [file.path, file.content, file.isBinary],
  );

  const initialMarkdownPreview = initialRenderedPreview && previewKind === "markdown";
  const [renderedPreview, setRenderedPreview] = useState(initialMarkdownPreview);
  const fileIdentity = `${file.repo ?? ""}\u0000${file.path}`;
  const [lastFileIdentity, setLastFileIdentity] = useState(fileIdentity);

  // Reset preview mode when the file changes so reopening a previewable file
  // always starts in editor view, not the previous preview state.
  // Adjust state during render per React docs recommendation.
  if (lastFileIdentity !== fileIdentity) {
    setLastFileIdentity(fileIdentity);
    setRenderedPreview(initialMarkdownPreview);
  }

  useEffect(() => {
    htmlPreview.reset();
  }, [fileIdentity, htmlPreview.reset]);

  const publishCurrentHtmlPreview = useCallback(() => {
    setRenderedPreview(true);
    void htmlPreview.publish({ path: file.path, repo: file.repo, content: file.content });
  }, [file.content, file.path, file.repo, htmlPreview.publish]);

  const sourceToggleIdentity = `${sessionId ?? ""} ${file.repo ?? ""} ${file.path}`;
  const sourceToggle = useTextImageSourceToggle(sourceToggleIdentity, !!file.isBinary, file.path);
  const showSource = viewerKind === "image" && sourceToggle.showSource;

  return (
    <PanelRoot data-testid="mobile-file-viewer-panel">
      <MobileFileViewerHeader
        file={file}
        sessionId={sessionId}
        activeTaskId={activeTaskId}
        repositoryId={repositoryId}
        fileStatus={fileStatus}
        viewerKind={viewerKind}
        previewKind={previewKind}
        renderedPreview={renderedPreview}
        htmlPreview={htmlPreview}
        sourcePreviewAction={
          showSource ? textImagePreviewAction(sourceToggle.showImage, t) : undefined
        }
        onDownload={onDownload}
        onPreviewHtml={publishCurrentHtmlPreview}
        onPreviewMarkdown={() => setRenderedPreview(true)}
        onClose={onClose}
      />
      <PanelBody padding={false} scroll={false} className="overflow-hidden">
        <MobileViewerBody
          file={file}
          viewerKind={viewerKind}
          previewKind={previewKind}
          renderedPreview={renderedPreview}
          worktreePath={worktreePath}
          sessionId={sessionId}
          taskId={activeTaskId}
          repositoryId={repositoryId}
          showSource={showSource}
          onShowSource={sourceToggle.enabled ? sourceToggle.showSourceCode : undefined}
          onTogglePreview={() => setRenderedPreview((current) => !current)}
          onRetryHtmlPreview={publishCurrentHtmlPreview}
          htmlPreview={htmlPreview}
        />
      </PanelBody>
    </PanelRoot>
  );
}
