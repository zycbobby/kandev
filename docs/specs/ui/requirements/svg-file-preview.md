---
status: active
system: ui
created: 2026-09-15
owners:
  - kandev
---

# SVG File Preview Requirements

## Overview

Kandev shall open a `.svg` file as a rendered image. SVG is XML text, so the
backend's UTF-8-based `is_binary` flag never marks it as binary; the previous
classification therefore routed every `.svg` into the source editor and the
image branch of the extension table was unreachable. This capability makes a
text-encoded image format reachable by the image renderer and gives the user an
explicit, reversible path between the rendered image and its source.

The UI system owns the classification rule, the viewer interaction, and the
toggle state. No backend, API, protocol, or persistence change is involved:
SVG arrives through the existing `workspace.file.get` response like any other
text file.

## Terminology

- **Text-encoded image format:** An image format whose file bytes are valid
  UTF-8 text, so the backend's `is_binary` flag is `false`. Currently SVG only.
- **Viewer kind:** The resolved viewer for a file (`image`, `binary`, or
  `text`), derived from the path extension and `is_binary` together.
- **Toggle state:** The session-local choice, per open file, between rendering
  the image and showing its source. It is never persisted.

## Requirements

### REQ-UI-SVG-FILE-PREVIEW-001: Classify text-encoded image formats as images

**Intent:** Let a format that is both valid text and a real image reach the
image renderer, without disturbing any file that is classified today.

#### Acceptance criteria

- **AC-UI-SVG-FILE-PREVIEW-001.1:** A file whose content is valid UTF-8 text
  and whose extension belongs to the text-encoded image formats shall be
  classified as an image and routed to the image renderer, not the source
  editor.
- **AC-UI-SVG-FILE-PREVIEW-001.2:** A file named with a binary image extension
  (for example `.png`) whose content is valid UTF-8 text shall remain
  classified as text and shall keep opening in the source editor.
- **AC-UI-SVG-FILE-PREVIEW-001.3:** A file whose content is not valid UTF-8
  shall keep its existing classification, including binary images rendering
  from their base64 payload.
- **AC-UI-SVG-FILE-PREVIEW-001.4:** The classification rule shall exist once.
  Every file-viewer entry point shall call the same exported resolver rather
  than restating the branches.

### REQ-UI-SVG-FILE-PREVIEW-002: Render SVG through a CSP-permitted data URL

**Intent:** Display the image on every deployment, including the desktop shell
whose content security policy restricts image sources.

#### Acceptance criteria

- **AC-UI-SVG-FILE-PREVIEW-002.1:** Image rendering shall produce a `data:`
  URL. It shall not depend on `blob:` URLs, because the desktop shell permits
  only `'self'` and `data:` for `img-src`.
- **AC-UI-SVG-FILE-PREVIEW-002.2:** For text content the renderer shall encode
  the UTF-8 bytes to base64 and build `data:image/svg+xml;base64,...`. For
  binary content it shall keep the existing
  `data:${mime};base64,${content}` form unchanged.
- **AC-UI-SVG-FILE-PREVIEW-002.3:** Encoding shall not throw or truncate for
  empty content, for content containing multibyte UTF-8 characters, or for
  content larger than a single encoding chunk.
- **AC-UI-SVG-FILE-PREVIEW-002.4:** The rendered image shall be contained by
  its viewer without distorting its aspect ratio, including when the SVG's
  intrinsic size exceeds the available area.

### REQ-UI-SVG-FILE-PREVIEW-003: Open on the image and offer a source round-trip

**Intent:** Match the expectation set by every other image format while keeping
the source reachable for inspection and editing.

#### Acceptance criteria

- **AC-UI-SVG-FILE-PREVIEW-003.1:** Opening a `.svg` shall render the image
  immediately, with no extra user action.
- **AC-UI-SVG-FILE-PREVIEW-003.2:** The rendered image shall expose an
  accessible `Show code` action. Activating it shall open the source editor
  with the file content and shall expose a preview action that returns to the
  image.
- **AC-UI-SVG-FILE-PREVIEW-003.3:** The source state shall retain the existing
  editing, saving, downloading, deleting, and external-viewer behavior with no
  change to those paths.
- **AC-UI-SVG-FILE-PREVIEW-003.4:** The toggle state shall be session-local and
  shall not persist. Reopening the same file shall render the image again, and
  a viewer host reused for a different file shall not inherit the previous
  file's state.

### REQ-UI-SVG-FILE-PREVIEW-004: Apply one rule across every entry point

**Intent:** Prevent the four file-viewer surfaces from drifting apart, which is
how SVG became unreachable in the first place.

#### Acceptance criteria

- **AC-UI-SVG-FILE-PREVIEW-004.1:** The desktop file tab, the dockview file
  panel, and the mobile file viewer shall share the same classification
  resolver, the same image rendering path, and the same toggle behavior.
- **AC-UI-SVG-FILE-PREVIEW-004.2:** A chat rich-output block that references a
  `.svg` shall render the image rather than the truncated text preview, and
  shall keep its existing open-file action as the route to the full viewer.
- **AC-UI-SVG-FILE-PREVIEW-004.3:** A chat rich-output block that references a
  binary non-image file shall keep showing the existing preview-unavailable
  copy, and one that references a text file shall keep the truncated text
  preview.

### REQ-UI-SVG-FILE-PREVIEW-005: Preserve desktop preview actions and mobile parity

**Intent:** Add the SVG control without regressing the Markdown and HTML
preview controls that share the same toolbar and header slots, and keep the
capability usable on a phone.

#### Acceptance criteria

- **AC-UI-SVG-FILE-PREVIEW-005.1:** The Markdown and HTML preview controls
  shall keep their existing labels, test identifiers, disabled states, and
  trusted-code disclosure. A file with no preview kind shall keep showing no
  preview control unless a host explicitly supplies one.
- **AC-UI-SVG-FILE-PREVIEW-005.2:** On phone and coarse-pointer surfaces the
  focused file viewer shall provide the same two states, with an active touch
  dimension of at least 44 pixels for both `Show code` and the action that
  returns to the image.
- **AC-UI-SVG-FILE-PREVIEW-005.3:** An SVG wider than the phone viewport shall
  be contained by the viewer and shall not introduce document-level horizontal
  overflow.

### REQ-UI-SVG-FILE-PREVIEW-006: Leave adjacent behavior unchanged

**Intent:** Keep this change scoped to the file-classification and viewer
interaction problem.

#### Acceptance criteria

- **AC-UI-SVG-FILE-PREVIEW-006.1:** Clicking a `.svg` entry in the Changes
  panel shall behave exactly as before. Its diff routing is a separate change.
- **AC-UI-SVG-FILE-PREVIEW-006.2:** No backend file-content response, API,
  protocol, or persistence schema shall change, and no dependency shall be
  added.
- **AC-UI-SVG-FILE-PREVIEW-006.3:** Markdown embedded-image rendering shall not
  change, and no SVG preview shall be attempted inside Markdown documents.

## Out of scope

- Full browser fidelity for `<foreignObject>` HTML content or SVG external
  references. Static `<img>` rendering is the deliberate trade that also
  removes the need for a trusted-code warning.
- Executing scripts contained in an SVG. An `<img>`-embedded SVG runs in the
  browser's secure static mode and does not execute script.
- A static server, port proxy, or iframe for SVG rendering.
- Changes-panel diff routing for `.svg` entries.
- Persisting the source/image choice across sessions or devices.
