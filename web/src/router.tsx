import { type AnchorHTMLAttributes, type MouseEvent, useSyncExternalStore } from "react";

const listeners = new Set<() => void>();

/** Subscribes to changes of the current location. */
function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  window.addEventListener("popstate", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("popstate", listener);
  };
}

/**
 * Moves to another page without reloading the application. The new page
 * starts at its top unless stay is set, which suits a change to what the
 * current page shows.
 */
export function navigate(to: string, stay = false): void {
  window.history.pushState(null, "", to);
  if (!stay) {
    window.scrollTo(0, 0);
  }
  listeners.forEach((listener) => listener());
}

/** Changes the address of the current page without adding a step to the history. */
export function replace(to: string): void {
  window.history.replaceState(null, "", to);
  listeners.forEach((listener) => listener());
}

/** Returns the query string of the current page. */
export function useSearch(): string {
  return useSyncExternalStore(subscribe, () => window.location.search);
}

/** Returns the path of the current page. */
export function usePath(): string {
  return useSyncExternalStore(subscribe, () => window.location.pathname);
}

/** A link that changes page without reloading the application. */
export function Link({ to, onClick, ...rest }: { to: string } & AnchorHTMLAttributes<HTMLAnchorElement>) {
  const follow = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event);
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) {
      return;
    }
    event.preventDefault();
    navigate(to);
  };
  return <a href={to} onClick={follow} {...rest} />;
}
