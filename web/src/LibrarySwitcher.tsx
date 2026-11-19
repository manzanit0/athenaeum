import { type KeyboardEvent, useEffect, useId, useRef, useState } from "react";
import type { Libraries, LibraryEntry } from "./api";
import { useOpener } from "./opening";
import { Link } from "./router";

const itemClass =
  "flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-ink/5 focus:bg-ink/5 focus:outline-none " +
  "disabled:cursor-not-allowed disabled:text-soft disabled:hover:bg-transparent";

/** Two chevrons, one up and one down: the mark of a control that opens a list of choices. */
function Chevrons() {
  return (
    <svg aria-hidden viewBox="0 0 10 16" className="h-4 w-2.5 shrink-0 text-soft" fill="none" stroke="currentColor">
      <path d="M1.5 6 5 2.5 8.5 6M1.5 10 5 13.5 8.5 10" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** A tick beside the open library, or the space it would take beside the others. */
function Tick({ shown }: { shown: boolean }) {
  return (
    <svg aria-hidden viewBox="0 0 12 12" className="h-3 w-3 shrink-0 text-leaf" fill="none" stroke="currentColor">
      {shown && <path d="M2 6.5 5 9.5 10 3" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />}
    </svg>
  );
}

/** One library in the menu: its name, a tick when it is open, a note when its folder is gone. */
function Choice({ library, onChoose }: { library: LibraryEntry; onChoose: () => void }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={library.state === "open"}
      disabled={library.state === "missing"}
      title={library.path}
      className={itemClass}
      onClick={onChoose}
    >
      <Tick shown={library.state === "open"} />
      <span className="min-w-0 flex-1 truncate">{library.name}</span>
      {library.state === "missing" && <span className="shrink-0 text-[0.87rem]">not found</span>}
    </button>
  );
}

/**
 * The control beside the application's name that says which library is
 * open. It opens a menu of the known libraries, where choosing one
 * switches to it, with the way to add or remove libraries below them.
 * The sections next to it belong to the library named here.
 */
export function LibrarySwitcher({ libraries, onRefused }: { libraries: Libraries; onRefused: () => void }) {
  const menuId = useId();
  const root = useRef<HTMLDivElement>(null);
  const [shown, setShown] = useState(false);
  const { open, dialog } = useOpener(onRefused);
  const current = libraries.libraries.find((library) => library.state === "open");
  const items = () => Array.from(root.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]:not(:disabled)') ?? []);

  useEffect(() => {
    if (!shown) {
      return;
    }
    const away = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) {
        setShown(false);
      }
    };
    document.addEventListener("pointerdown", away);
    const all = items();
    (all.find((item) => item.getAttribute("aria-checked") === "true") ?? all[0])?.focus();
    return () => document.removeEventListener("pointerdown", away);
  }, [shown]);

  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape" && shown) {
      setShown(false);
      root.current?.querySelector<HTMLElement>("[aria-haspopup]")?.focus();
      return;
    }
    const step = { ArrowDown: 1, ArrowUp: -1 }[event.key];
    if (!shown || step === undefined) {
      return;
    }
    event.preventDefault();
    const all = items();
    const at = all.indexOf(document.activeElement as HTMLElement);
    all[(at + step + all.length) % all.length]?.focus();
  };

  const choose = (library: LibraryEntry) => {
    setShown(false);
    if (library.state !== "open") {
      open({ path: library.path, create: false });
    }
  };

  return (
    <div ref={root} className="relative" onKeyDown={keys}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={shown}
        aria-controls={shown ? menuId : undefined}
        className={
          "flex w-full items-center justify-between gap-2 rounded-sm border bg-sheet px-3 py-2 text-left " +
          `hover:border-ink ${shown ? "border-ink" : "border-rule"}`
        }
        onClick={() => setShown(!shown)}
      >
        <span className="min-w-0 truncate font-medium">
          <span className="sr-only">Library: </span>
          {current?.name ?? "No library open"}
        </span>
        <Chevrons />
      </button>
      {shown && (
        <div
          id={menuId}
          role="menu"
          aria-label="Libraries"
          className="absolute left-0 top-full z-40 mt-1 w-64 max-w-[calc(100vw-2.5rem)] rounded-sm border border-ink bg-sheet py-1 shadow-[0_6px_0_-3px_rgb(27_33_30/0.18)]"
        >
          {libraries.libraries.map((library) => (
            <Choice key={library.path} library={library} onChoose={() => choose(library)} />
          ))}
          <Link
            to="/libraries"
            role="menuitem"
            className={`${itemClass} mt-1 border-t border-rule pt-2.5`}
            onClick={() => setShown(false)}
          >
            <Tick shown={false} />
            Add or remove libraries…
          </Link>
        </div>
      )}
      {dialog}
    </div>
  );
}
