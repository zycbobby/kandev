"use client";

import { TabsContent } from "@kandev/ui/tabs";
import { useTranslation } from "react-i18next";
import { FileEditorContent } from "./file-editor-content";
import { FileImageViewer } from "./file-image-viewer";
import { FileBinaryViewer } from "./file-binary-viewer";
import type { OpenFileTab } from "@/lib/types/backend";
import {
  getFilePreviewKind,
  resolveFileViewerKind,
  type FilePreviewKind,
  type FileViewerKind,
} from "@/lib/utils/file-types";
import { getSessionWorkspacePath } from "@/lib/session-workspace-path";
import { FileViewerExternalLink } from "./file-viewer-header";
import { getFileTabKey } from "./task-center-panel-file-tabs";
import { textImagePreviewAction, type PreviewAction } from "@/components/editors/preview-action";
import { useTextImageSourceToggle } from "@/hooks/use-text-image-source-toggle";

type SourceToggle = ReturnType<typeof useTextImageSourceToggle>;

type TabView = {
  viewerKind: FileViewerKind;
  previewKind: FilePreviewKind;
  showImage: boolean;
  showSource: boolean;
  onShowSource?: () => void;
  sourcePreviewAction?: PreviewAction;
  renderedPreview: boolean;
  onTogglePreview?: () => void;
};

function resolveTabView(
  tab: OpenFileTab,
  sourceToggle: SourceToggle,
  t: (key: string) => string,
  onTogglePreview?: () => void,
): TabView {
  const viewerKind = resolveFileViewerKind(tab.path, !!tab.isBinary);
  const previewKind = getFilePreviewKind(tab.path, !!tab.isBinary);
  const showSource = viewerKind === "image" && sourceToggle.showSource;
  const isMarkdown = previewKind === "markdown";
  return {
    viewerKind,
    previewKind,
    showImage: viewerKind === "image" && !sourceToggle.showSource,
    showSource,
    onShowSource: sourceToggle.enabled ? sourceToggle.showSourceCode : undefined,
    sourcePreviewAction: showSource ? textImagePreviewAction(sourceToggle.showImage, t) : undefined,
    renderedPreview: isMarkdown && !!tab.renderedPreview,
    onTogglePreview: isMarkdown ? onTogglePreview : undefined,
  };
}

export function FileTabContent({
  tab,
  activeSession,
  activeSessionId,
  taskId,
  isSaving,
  onFileChange,
  onFileSave,
  onFileDelete,
  onTogglePreview,
}: {
  tab: OpenFileTab;
  activeSession: {
    workspace_path?: string | null;
    worktree_path?: string | null;
    repository_id?: string | null;
  } | null;
  activeSessionId: string | null;
  taskId?: string | null;
  isSaving: boolean;
  onFileChange: (path: string, content: string, repo?: string) => void;
  onFileSave: (path: string, repo?: string) => void;
  onFileDelete: (path: string, repo?: string) => void;
  onTogglePreview?: () => void;
}) {
  const { t } = useTranslation();
  const workspacePath = getSessionWorkspacePath(activeSession);
  const fileIdentity = `${activeSessionId ?? ""}\u0000${tab.repo ?? ""}\u0000${tab.path}`;
  const sourceToggle = useTextImageSourceToggle(fileIdentity, !!tab.isBinary, tab.path);
  const view = resolveTabView(tab, sourceToggle, t, onTogglePreview);
  const externalLink = (
    <FileViewerExternalLink
      path={tab.path}
      sessionId={activeSessionId}
      taskId={taskId}
      repositoryId={activeSession?.repository_id}
      repositoryName={tab.repo}
    />
  );

  return (
    <TabsContent value={`file:${getFileTabKey(tab)}`} className="flex-1 min-h-0">
      {view.showImage && (
        <FileImageViewer
          isSymlink={!!tab.resolvedPath}
          path={tab.path}
          content={tab.content}
          isBinary={!!tab.isBinary}
          worktreePath={workspacePath}
          headerActions={externalLink}
          onShowSource={view.onShowSource}
        />
      )}
      {view.viewerKind === "binary" && (
        <FileBinaryViewer
          isSymlink={!!tab.resolvedPath}
          path={tab.path}
          worktreePath={workspacePath}
          headerActions={externalLink}
        />
      )}
      {(view.viewerKind === "text" || view.showSource) && (
        <FileEditorContent
          isSymlink={!!tab.resolvedPath}
          path={tab.path}
          content={tab.content}
          originalContent={tab.originalContent}
          isDirty={tab.isDirty}
          isSaving={isSaving}
          sessionId={activeSessionId || undefined}
          taskId={taskId}
          repositoryId={activeSession?.repository_id ?? undefined}
          worktreePath={workspacePath}
          repo={tab.repo}
          enableComments={!!activeSessionId}
          previewKind={view.previewKind}
          renderedPreview={view.renderedPreview}
          onTogglePreview={view.onTogglePreview}
          sourcePreviewAction={view.sourcePreviewAction}
          onChange={(newContent) => onFileChange(tab.path, newContent, tab.repo)}
          onSave={() => onFileSave(tab.path, tab.repo)}
          onDelete={() => onFileDelete(tab.path, tab.repo)}
        />
      )}
    </TabsContent>
  );
}
