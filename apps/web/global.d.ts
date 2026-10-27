export {};

declare global {
  interface KandevWindowControlsOverlay {
    readonly visible: boolean;
    getTitlebarAreaRect(): DOMRectReadOnly;
    addEventListener(type: "geometrychange", listener: EventListener): void;
    removeEventListener(type: "geometrychange", listener: EventListener): void;
  }

  interface Navigator {
    readonly windowControlsOverlay?: KandevWindowControlsOverlay;
  }

  interface Window {
    // Port injection for dev mode (browser on web port, API on backend port);
    // while viewing a remote instance it carries that instance's proxy port.
    __KANDEV_API_PORT?: string;
    // The selected remote instance's ID, injected by the Go shell alongside
    // the proxy port. Absent on a local boot; the SPA treats that as 'local'.
    __KANDEV_INSTANCE_ID?: string;
    // Debug mode flag (injected by the Go shell or derived from boot payload runtime config)
    __KANDEV_DEBUG?: boolean;
  }
}
