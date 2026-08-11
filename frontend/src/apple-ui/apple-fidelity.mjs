/* Apple fidelity presentation runtime */
const instances = new WeakMap();

const SIZE_CLASSES = Object.freeze({
  compact: 600,
  regular: 1024,
});

function isDocumentRoot(root) {
  return root?.nodeType === 9;
}

function queryScope(root) {
  return isDocumentRoot(root) ? root : root.ownerDocument || document;
}

function queryAll(root, selector) {
  return Array.from(root.querySelectorAll(selector));
}

function closestFromEvent(event, selector, root) {
  const candidate = typeof event.target?.closest === "function"
    ? event.target.closest(selector)
    : null;
  if (!candidate) return null;
  if (isDocumentRoot(root) || root === candidate || root.contains(candidate)) {
    return candidate;
  }
  return null;
}

function classifyWidth(width) {
  if (width < SIZE_CLASSES.compact) return "compact";
  if (width < SIZE_CLASSES.regular) return "regular";
  return "expanded";
}

function edgeStrength(distance) {
  if (distance <= 0.5) return "none";
  if (distance < 28) return "soft";
  return "hard";
}

function targetsForScroller(root, source) {
  const selector = source.getAttribute("data-aui-scroll-edge-target");
  if (selector) {
    try {
      return queryAll(root, selector);
    } catch {
      return [];
    }
  }
  return [source.closest("[data-aui-scroll-edge-container]") || source];
}

/**
 * Add Apple Fidelity presentation behavior without taking ownership of product
 * selection, routing, form, dialog, or persistence state.
 */
export function initAppleFidelity(root = document) {
  if (!root || ![1, 9].includes(root.nodeType)) {
    throw new TypeError("initAppleFidelity expects a Document or Element root");
  }

  const existing = instances.get(root);
  if (existing) return existing;

  const doc = queryScope(root);
  const controller = new doc.defaultView.AbortController();
  const { signal } = controller;
  const resizeTarget = isDocumentRoot(root) ? root.documentElement : root;
  const snapshots = new Map();
  const observers = [];
  const animationFrames = new Map();
  const pressed = new Set();
  const pointerOwners = new Map();

  const snapshotFor = (element) => {
    let snapshot = snapshots.get(element);
    if (!snapshot) {
      snapshot = {
        attributes: new Map(),
        styles: new Map(),
      };
      snapshots.set(element, snapshot);
    }
    return snapshot;
  };

  const rememberAttribute = (element, name) => {
    const { attributes } = snapshotFor(element);
    if (!attributes.has(name)) {
      attributes.set(name, element.getAttribute(name));
    }
  };

  const rememberStyle = (element, name) => {
    const { styles } = snapshotFor(element);
    if (!styles.has(name)) {
      styles.set(name, {
        value: element.style.getPropertyValue(name),
        priority: element.style.getPropertyPriority(name),
      });
    }
  };

  const setRuntimeAttribute = (element, name, value) => {
    rememberAttribute(element, name);
    if (value === null) element.removeAttribute(name);
    else element.setAttribute(name, value);
  };

  const restoreRuntimeAttribute = (element, name) => {
    const previous = snapshots.get(element)?.attributes.get(name);
    if (previous === undefined || previous === null) {
      element.removeAttribute(name);
    } else {
      element.setAttribute(name, previous);
    }
  };

  const setRuntimeStyle = (element, name, value) => {
    rememberStyle(element, name);
    element.style.setProperty(name, value);
  };

  const clearPressed = (element) => {
    if (!element) return;
    restoreRuntimeAttribute(element, "data-aui-pressed");
    pressed.delete(element);
  };

  const clearAllPressed = () => {
    for (const [pointerId, element] of pointerOwners) {
      try {
        if (element.hasPointerCapture?.(pointerId)) {
          element.releasePointerCapture(pointerId);
        }
      } catch {
        // Capture may already have been released by the browser.
      }
    }
    for (const element of Array.from(pressed)) clearPressed(element);
    pointerOwners.clear();
  };

  const updatePointerLight = (element, event) => {
    const rect = element.getBoundingClientRect();
    if (!rect.width || !rect.height) return;
    const x = Math.max(0, Math.min(100, ((event.clientX - rect.left) / rect.width) * 100));
    const y = Math.max(0, Math.min(100, ((event.clientY - rect.top) / rect.height) * 100));
    setRuntimeStyle(element, "--aui-pointer-x", `${x.toFixed(2)}%`);
    setRuntimeStyle(element, "--aui-pointer-y", `${y.toFixed(2)}%`);
    // A small translated lens cue makes the highlight follow the contact
    // point without rotating the product surface or stealing layout space.
    const shiftX = ((x - 50) / 50) * 4;
    const shiftY = ((y - 50) / 50) * 4;
    setRuntimeStyle(element, "--aui-lens-shift-x", `${shiftX.toFixed(2)}px`);
    setRuntimeStyle(element, "--aui-lens-shift-y", `${shiftY.toFixed(2)}px`);
  };

  const onPointerMove = (event) => {
    const element = closestFromEvent(
      event,
      "[data-aui-pressable], [data-aui-layer='functional']",
      root,
    );
    if (element) updatePointerLight(element, event);
  };

  const onPointerDown = (event) => {
    if (event.button !== 0) return;
    const element = closestFromEvent(event, "[data-aui-pressable]", root);
    if (!element || element.matches(":disabled, [aria-disabled='true']")) return;
    updatePointerLight(element, event);
    const previousOwner = pointerOwners.get(event.pointerId);
    if (previousOwner && previousOwner !== element) clearPressed(previousOwner);
    pointerOwners.set(event.pointerId, element);
    setRuntimeAttribute(element, "data-aui-pressed", "");
    pressed.add(element);
    try {
      element.setPointerCapture?.(event.pointerId);
    } catch {
      // Window-level pointer end listeners still guarantee cleanup.
    }
  };

  const onPointerEnd = (event) => {
    const owner = pointerOwners.get(event.pointerId);
    pointerOwners.delete(event.pointerId);
    const element = owner || closestFromEvent(event, "[data-aui-pressable]", root);
    if (!element) return;
    try {
      if (element.hasPointerCapture?.(event.pointerId)) {
        element.releasePointerCapture(event.pointerId);
      }
    } catch {
      // Capture may already have been released by the browser.
    }
    clearPressed(element);
  };

  const onKeyDown = (event) => {
    if (event.repeat || (event.key !== " " && event.key !== "Enter")) return;
    const element = closestFromEvent(event, "[data-aui-pressable]", root);
    if (!element || element.matches(":disabled, [aria-disabled='true']")) return;
    setRuntimeAttribute(element, "data-aui-pressed", "");
    pressed.add(element);
  };

  const onKeyUp = (event) => {
    if (event.key !== " " && event.key !== "Enter") return;
    clearPressed(closestFromEvent(event, "[data-aui-pressable]", root));
  };

  const updateSizeClass = () => {
    const width = resizeTarget.getBoundingClientRect().width || doc.defaultView?.innerWidth || 0;
    setRuntimeAttribute(resizeTarget, "data-aui-size-class", classifyWidth(width));
  };

  const scheduleEdgeUpdate = (source) => {
    const previous = animationFrames.get(source);
    if (previous !== undefined) return;
    const frame = doc.defaultView.requestAnimationFrame(() => {
      animationFrames.delete(source);
      const strength = edgeStrength(source.scrollTop);
      for (const target of targetsForScroller(root, source)) {
        setRuntimeAttribute(target, "data-aui-scroll-edge-state", strength);
      }
    });
    animationFrames.set(source, frame);
  };

  const bindScrollEdges = () => {
    for (const source of queryAll(root, "[data-aui-scroll-edge-source]")) {
      if (source.hasAttribute("data-aui-scroll-edge-bound")) continue;
      if (!targetsForScroller(root, source).length) continue;
      setRuntimeAttribute(source, "data-aui-scroll-edge-bound", "");
      const update = () => scheduleEdgeUpdate(source);
      source.addEventListener("scroll", update, { passive: true, signal });
      update();

      if ("ResizeObserver" in doc.defaultView) {
        const observer = new doc.defaultView.ResizeObserver(update);
        observer.observe(source);
        observers.push(observer);
      }
    }
  };

  const bindDynamicContent = () => {
    if (!("MutationObserver" in doc.defaultView)) return;
    const observer = new doc.defaultView.MutationObserver(() => {
      if (signal.aborted) return;
      bindScrollEdges();
      updateSizeClass();
    });
    observer.observe(root, { childList: true, subtree: true });
    observers.push(observer);
  };

  root.addEventListener("pointermove", onPointerMove, { passive: true, signal });
  root.addEventListener("pointerdown", onPointerDown, { passive: true, signal });
  root.addEventListener("pointerup", onPointerEnd, { passive: true, signal });
  root.addEventListener("pointercancel", onPointerEnd, { passive: true, signal });
  root.addEventListener("lostpointercapture", onPointerEnd, { passive: true, signal });
  root.addEventListener("keydown", onKeyDown, { signal });
  root.addEventListener("keyup", onKeyUp, { signal });
  root.addEventListener("focusout", (event) => {
    if (event.target?.nodeType === 1) clearPressed(event.target);
  }, { signal });
  doc.defaultView.addEventListener("blur", clearAllPressed, { signal });
  doc.defaultView.addEventListener("pointerup", onPointerEnd, {
    passive: true,
    signal,
  });
  doc.defaultView.addEventListener("pointercancel", onPointerEnd, {
    passive: true,
    signal,
  });

  if ("ResizeObserver" in doc.defaultView) {
    const observer = new doc.defaultView.ResizeObserver(updateSizeClass);
    observer.observe(resizeTarget);
    observers.push(observer);
  } else {
    doc.defaultView.addEventListener("resize", updateSizeClass, { passive: true, signal });
  }

  const supportsBackdrop = doc.defaultView.CSS?.supports?.(
    "backdrop-filter",
    "blur(1px)",
  ) || globalThis.CSS?.supports?.("-webkit-backdrop-filter", "blur(1px)");
  setRuntimeAttribute(resizeTarget, "data-aui-no-backdrop", supportsBackdrop ? null : "");
  updateSizeClass();
  bindScrollEdges();
  bindDynamicContent();

  const api = Object.freeze({
    signal,
    refresh() {
      if (signal.aborted) return;
      updateSizeClass();
      bindScrollEdges();
      for (const source of queryAll(root, "[data-aui-scroll-edge-source]")) {
        if (targetsForScroller(root, source).length) {
          scheduleEdgeUpdate(source);
        }
      }
    },
    destroy() {
      if (signal.aborted) return;
      controller.abort();
      for (const observer of observers) observer.disconnect();
      for (const [source, frame] of animationFrames) {
        doc.defaultView.cancelAnimationFrame(frame);
        animationFrames.delete(source);
      }
      clearAllPressed();
      for (const [element, snapshot] of snapshots) {
        const { attributes, styles } = snapshot;
        for (const [name, previous] of attributes) {
          if (previous === null) element.removeAttribute(name);
          else element.setAttribute(name, previous);
        }
        for (const [name, previous] of styles) {
          if (!previous.value) element.style.removeProperty(name);
          else {
            element.style.setProperty(
              name,
              previous.value,
              previous.priority,
            );
          }
        }
      }
      instances.delete(root);
    },
  });

  instances.set(root, api);
  return api;
}

if (typeof document !== "undefined") {
  const autoInit = () => {
    const root = document.querySelector("[data-aui-fidelity='apple-fidelity-web/v1']") || document;
    initAppleFidelity(root);
  };
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", autoInit, { once: true });
  } else {
    autoInit();
  }
}
