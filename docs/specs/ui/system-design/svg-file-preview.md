---
status: current
system: ui
requirements:
  - REQ-UI-SVG-FILE-PREVIEW-001
  - REQ-UI-SVG-FILE-PREVIEW-002
  - REQ-UI-SVG-FILE-PREVIEW-003
  - REQ-UI-SVG-FILE-PREVIEW-004
  - REQ-UI-SVG-FILE-PREVIEW-005
  - REQ-UI-SVG-FILE-PREVIEW-006
---

# SVG File Preview System Design

## Purpose and boundaries

SVG preview reuses the existing image viewer instead of adding a rendering
subsystem. The change is confined to the frontend: one exported classifier
decides which viewer a file gets, one shared hook holds the source/image
toggle, and the existing toolbar and viewer-header action slots gain one
descriptor-driven control.

The feature deliberately does not introduce a static server, a port proxy, or
an iframe, and it makes no attempt at full browser fidelity. That is the same
choice that removes the need for a trusted-code warning: an `<img>`-embedded
SVG runs in the browser's secure static mode, so script does not execute and
external resources do not load. SVG therefore behaves as an image, not as a
document to be trusted.

Nothing in this design changes a backend response. SVG arrives as ordinary
UTF-8 text through `workspace.file.get`, which is precisely why the
classification fix belongs in the frontend.

## Requirement mapping

| Requirement                      | Design sections                                                                                                                                                                |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `REQ-UI-SVG-FILE-PREVIEW-001`    | [Architecture](#architecture), [Classification](#classification), [Verification strategy](#verification-strategy)                                                              |
| `REQ-UI-SVG-FILE-PREVIEW-002`    | [Source encoding](#source-encoding), [Verification strategy](#verification-strategy)                                                                                            |
| `REQ-UI-SVG-FILE-PREVIEW-003`    | [Toggle state](#toggle-state), [Desktop controls](#desktop-controls), [Verification strategy](#verification-strategy)                                                           |
| `REQ-UI-SVG-FILE-PREVIEW-004`    | [Entry points](#entry-points), [Chat rich-output block](#chat-rich-output-block), [Verification strategy](#verification-strategy)                                               |
| `REQ-UI-SVG-FILE-PREVIEW-005`    | [Desktop controls](#desktop-controls), [Responsive contract](#responsive-contract), [Failure and recovery](#failure-and-recovery)                                               |
| `REQ-UI-SVG-FILE-PREVIEW-006`    | [Boundaries with adjacent features](#boundaries-with-adjacent-features), [Verification strategy](#verification-strategy)                                                        |

## Architecture

```text
workspace.file.get  (unchanged; is_binary = false for SVG)
        |
        v
resolveFileViewerKind(path, isBinary)        lib/utils/file-types.ts
        |  image | binary | text
        |
        +--> image ---> buildImageSrc(path, content, isBinary)
        |                   |  binary: pass through backend base64
        |                   `  text:   utf8ToBase64 -> data:image/svg+xml;base64,...
        |              FileImageViewer  <img data-testid="image-viewer">
        |                   |  Show code  ->  useTextImageSourceToggle
        |                   v
        `--> text/source --> FileEditorContent -> editor toolbar
                                 ` Preview SVG -> toggle back to the image
```

The renderer is the pre-existing `FileImageViewer`; SVG is not a new viewer.
The only new primitives are a classifier, a base64 encoder for text payloads,
a toggle hook, and one toolbar action descriptor.

## Classification

`resolveFileViewerKind(path, isBinary)` replaces four copies of the same
branching logic. It returns:

1. `image` when the extension is an image format **and** the content is binary
   **or** the extension is a text-encoded image format;
2. `binary` when the content is binary;
3. `text` otherwise.

The order matters. Hoisting "extension is an image" above the binary test
would be simpler but regresses a text file that merely carries a `.png` name,
turning a readable file into a broken image. The whitelist
(`TEXT_IMAGE_EXTENSIONS`, currently `svg`) is what keeps the fix additive: only
formats whose bytes really are an image gain the new branch.

`previewKind` is deliberately **not** extended with an `"svg"` value. That
mechanism means "source first, preview on request", which is the opposite of
the required default, and its `=== "markdown"` comparisons are persisted in
several places.

## Source encoding

`buildImageSrc(path, content, isBinary)` always returns
`data:${mime};base64,${payload}`. Binary content is already base64 from the
backend and passes through. Text content is encoded by `utf8ToBase64`, which
runs `TextEncoder`, converts to a binary string in `0x8000`-byte chunks, and
calls `btoa`.

Chunking is required, not stylistic: a spread of a large byte array overflows
the call stack. A `data:` URL is also required rather than merely convenient:
the desktop shell's policy allows only `'self'` and `data:` for `img-src`, so
a `blob:` URL is blocked. The `encodeURIComponent` form was rejected because
XML markup expands unpredictably under percent-encoding, whereas base64
inflates by a fixed 1.33x.

## Toggle state

`useTextImageSourceToggle(fileIdentity, isBinary, path)` owns one boolean and
exposes `enabled`, `showSource`, `showSourceCode`, and `showImage`. It is
session-local by decision: the choice is not written to the dockview store and
not persisted, so reopening a file always starts on the image.

The hook resets when `fileIdentity` changes, which is the guard for viewer
hosts that outlive the file they display — a dockview panel is reused when a
different file is opened into it. The reset happens during render rather than
in an effect so the stale state cannot paint for a frame. `enabled` is false
for anything that is not a text-encoded image, so binary images and plain text
never expose a toggle.

## Desktop controls

The editor toolbar already had a hard gate: `previewKind === "none"` returned
`null` before any action was considered. SVG's `previewKind` is `"none"`, so
the return path had nowhere to mount, and the button's label, test identifier,
disabled state, and trust warning were selected by an `isHtml ? A : B`
ternary that lint forbids from growing a third arm.

The design therefore replaces the ternary with an action descriptor resolved by
`resolvePreviewAction`. A descriptor carries the label, test identifier, click
handler, and optional disabled, busy, and trust-warning fields. The Markdown
and HTML paths resolve into descriptors exactly as before, which keeps their
observable behavior identical, and a new optional `sourcePreviewAction` prop
lets a host supply a descriptor directly. `sourcePreviewAction` wins outright
because only a host outside the preview-kind mechanism sets it.

The descriptor travels from the file viewer host through `FileEditorContent`
into whichever editor is configured, then into the toolbar. Both the Monaco and
CodeMirror toolbars render the same `PreviewActionButton`, so the two editors
cannot drift.

## Entry points

Four surfaces classify files, and all four now call the same resolver:

| Entry point                      | Image state                    | Source state                          |
| -------------------------------- | ------------------------------ | ------------------------------------- |
| Desktop file tab                 | `FileImageViewer` + `Show code` | `FileEditorContent` + `Preview SVG`   |
| Dockview file panel              | `FileImageViewer` + `Show code` | `FileEditorContent` + `Preview SVG`   |
| Mobile file viewer               | `FileImageViewer` + `Show code` | read-only `FileViewerContent` + header action |
| Chat rich-output file block      | inline `<img>`, no toggle      | existing open-file action             |

The mobile viewer's source state is the existing read-only CodeMirror body,
which has no toolbar. Its return control therefore renders in the mobile
viewer header, next to the download and preview controls, using the same
descriptor. This is a documented deviation from the toolbar path rather than a
second mechanism: the descriptor, the label, and the test identifier are
shared, and only the host of the button differs, because the mobile source
surface has no toolbar to receive a prop.

## Chat rich-output block

The block resolves the image case before the binary case, so a text-encoded SVG
renders as an `<img>` instead of the truncated `<pre>`. Non-image binaries keep
the preview-unavailable copy and text files keep the truncated preview. The
block gains no source state: it is a preview, and its existing open-file action
already leads to the full viewer where `Show code` lives.

## Responsive contract

Both SVG actions must be reachable on a coarse pointer:

- `Show code` lives in the image viewer header and uses the header's touch
  sizing (`h-11`, 44px) with a desktop override, matching the download control
  beside it.
- The mobile header's return-to-image control is touch-sized unconditionally;
  the desktop toolbar's is not, per the repository's rule that touch sizing
  stays on touch surfaces.

The image itself is constrained by `max-w-full max-h-full object-contain`
inside an overflow container, so an SVG wider than a phone viewport is
contained rather than widening the document.

## Boundaries with adjacent features

- The Changes panel keeps its current routing for `.svg` rows. Diff routing is
  a separate change.
- Markdown embedded images and relative-path rendering are untouched.
- No backend response, API, protocol, or persistence schema changes, and no
  dependency is added.
- No trust warning is added, because `<img>`-embedded SVG does not execute
  script. This is the deliberate difference from native HTML preview, which
  does run workspace code and therefore warns.

## Failure and recovery

- **Image fails to decode.** The `<img>` renders as a broken image inside the
  viewer. The user still reaches the source through `Show code`, which is
  always available in the image state for a text-encoded image, so a
  malformed SVG is never a dead end.
- **Content is not a valid SVG.** It renders as a broken image with the source
  one action away. The file is not reclassified; extension and encoding, not
  parseability, drive the viewer choice.
- **A stale toggle state after a host is reused.** The `fileIdentity` reset
  returns the viewer to the image state.
- **Large SVG.** Fixed 1.33x inflation on top of the existing 10MB file limit,
  with chunked encoding to avoid a stack overflow.

## Verification strategy

The deciding risks are only observable in a real browser, so end-to-end
coverage is the acceptance gate and unit tests cover the pure functions:

- SVG rendering depends on image decoding and on the desktop shell's policy,
  which jsdom does not implement. The desktop spec therefore asserts the image
  is present **and** that `naturalWidth > 0`, proving a real decode rather than
  a broken-image placeholder.
- The toolbar change touches the Markdown and HTML preview controls that share
  the slot. Their labels and test identifiers are asserted unchanged, and the
  existing preview specs act as the regression net.
- Touch sizing and horizontal containment are layout behaviors; the mobile spec
  asserts the 44px bounding box and the document overflow check.
- Classification and encoding are pure functions and are covered by unit tests,
  including the non-regression branch where a `.png` file holds text, and the
  multibyte and over-chunk-size encoding cases.
