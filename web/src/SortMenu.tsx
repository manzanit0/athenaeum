import { type KeyboardEvent, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { directions, sortFields, sortOf } from "./shelf";

/** A tick beside the choice in use, or the space it would take beside the others. */
function Tick({ shown }: { shown: boolean }) {
  return (
    <svg aria-hidden viewBox="0 0 12 12" className="h-3 w-3 shrink-0" fill="none" stroke="currentColor">
      {shown && <path d="M2 6.5 5 9.5 10 3" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />}
    </svg>
  );
}

/** One choice of the menu, ticked when it is the one in use. */
function Choice({ label, chosen, onChoose }: { label: string; chosen: boolean; onChoose: () => void }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={chosen}
      onClick={onChoose}
      className={
        "flex w-full items-center gap-2.5 px-3.5 py-1.5 text-left hover:bg-ink/5 focus:bg-ink/5 focus:outline-none " +
        (chosen ? "font-semibold" : "")
      }
    >
      <Tick shown={chosen} />
      {label}
    </button>
  );
}

/** The choices of one kind under their name, with a rule above when they follow another kind. */
function Group({ title, ruled = false, children }: { title: string; ruled?: boolean; children: ReactNode }) {
  return (
    <div role="group" aria-label={title} className={`py-1.5 ${ruled ? "border-t border-rule" : ""}`}>
      <p aria-hidden className="px-3.5 pb-0.5 pt-1 text-[0.87rem] text-soft">
        {title}
      </p>
      {children}
    </div>
  );
}

/**
 * The button that opens the ways to put the shelf in order: the
 * property to order by and, under it, the direction. Choosing a
 * property takes its usual direction, A to Z or the newest first.
 */
export function SortMenu({ sort, onSort }: { sort: string; onSort: (sort: string) => void }) {
  const menuId = useId();
  const root = useRef<HTMLDivElement>(null);
  const [shown, setShown] = useState(false);
  const [field] = sort.split(":");
  const items = () => Array.from(root.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? []);

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
      event.stopPropagation();
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

  const choose = (next: string) => {
    setShown(false);
    onSort(next);
  };

  return (
    <div ref={root} className="relative" onKeyDown={keys}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={shown}
        aria-controls={shown ? menuId : undefined}
        onClick={() => setShown(!shown)}
        className={
          "inline-flex h-[2.6rem] items-center gap-2 rounded-sm border bg-sheet px-3.5 font-medium hover:border-ink " +
          (shown ? "border-ink" : "border-rule")
        }
      >
        Sort
        <svg aria-hidden viewBox="0 0 12 12" className="h-3 w-3 shrink-0" fill="none" stroke="currentColor">
          <path d="M2.5 4.5 6 8l3.5-3.5" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {shown && (
        <div
          id={menuId}
          role="menu"
          aria-label="Sort"
          className="absolute right-0 top-full z-40 mt-1 w-56 max-w-[calc(100vw-2.5rem)] rounded-sm border border-ink bg-sheet shadow-[0_6px_0_-3px_rgb(27_33_30/0.18)]"
        >
          <Group title="Sort by">
            {sortFields.map((entry) => (
              <Choice
                key={entry.field}
                label={entry.label}
                chosen={entry.field === field}
                onChoose={() => choose(entry.field === field ? sort : sortOf(entry.field))}
              />
            ))}
          </Group>
          <Group title="Order" ruled>
            {directions(field).map((direction) => (
              <Choice
                key={direction.sort}
                label={direction.label}
                chosen={direction.sort === sort}
                onChoose={() => choose(direction.sort)}
              />
            ))}
          </Group>
        </div>
      )}
    </div>
  );
}
