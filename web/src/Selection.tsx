import { type KeyboardEvent, useEffect, useId, useRef, useState } from "react";
import type { Tally } from "./api";
import { count } from "./format";
import { Link } from "./router";
import { Button, buttonClass } from "./ui";

/**
 * The button that files the ticked books under a category, or takes
 * them out of one. It opens a small panel above the bar with the name
 * of the category, which the library's categories are suggested for.
 */
function Categorise({
  ticked,
  known,
  onTag,
}: {
  ticked: number;
  known: Tally[];
  onTag: (add: string[], remove: string[]) => Promise<boolean>;
}) {
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLInputElement>(null);
  const [shown, setShown] = useState(false);
  const [tag, setTag] = useState("");
  const name = tag.trim();
  const books = count(ticked, "book");

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
    field.current?.focus();
    return () => document.removeEventListener("pointerdown", away);
  }, [shown]);

  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape" && shown) {
      event.stopPropagation();
      setShown(false);
      root.current?.querySelector<HTMLElement>("[aria-haspopup]")?.focus();
    }
  };
  const file = async (add: string[], remove: string[]) => {
    if (await onTag(add, remove)) {
      setTag("");
      setShown(false);
    }
  };

  return (
    <div ref={root} className="relative" onKeyDown={keys}>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={shown}
        aria-controls={shown ? `${id}-panel` : undefined}
        onClick={() => setShown(!shown)}
        className={buttonClass()}
      >
        Categorise
        <svg aria-hidden viewBox="0 0 12 12" className="h-3 w-3 shrink-0" fill="none" stroke="currentColor">
          <path d="M2.5 4.5 6 8l3.5-3.5" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {shown && (
        <div
          id={`${id}-panel`}
          role="dialog"
          aria-label="Categorise"
          className="absolute bottom-full left-0 z-20 mb-2 w-[26rem] max-w-[calc(100vw-3.5rem)] rounded-sm border border-ink bg-sheet p-4 shadow-[0_10px_24px_-12px_rgb(27_33_30/0.6)]"
        >
          <label htmlFor={id} className="mb-1 block font-medium">
            Category
          </label>
          <input
            id={id}
            ref={field}
            list={`${id}-known`}
            value={tag}
            onChange={(event) => setTag(event.target.value)}
            placeholder="History / Military"
            className="w-full rounded-sm border border-rule bg-paper px-3 py-2 focus:border-ink focus:outline-none focus:ring-2 focus:ring-ribbon/40"
          />
          <datalist id={`${id}-known`}>
            {known.map((entry) => (
              <option key={entry.value} value={entry.value} />
            ))}
          </datalist>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button variant="primary" disabled={name === ""} onClick={() => file([name], [])}>
              Add to {books}
            </Button>
            <Button disabled={name === ""} onClick={() => file([], [name])}>
              Remove from {books}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * The bar of what can be done with the ticked books: file them under a
 * category or send them to the Kindle. It is there while a book is
 * ticked. When the reader came to choose books for the Kindle, it is
 * there from the start and says so, and leaving it goes back to the
 * Kindle.
 */
export function Selection({
  ticked,
  shown,
  known,
  sending,
  busy,
  connected,
  onAll,
  onNone,
  onTag,
  onSend,
  onLeave,
}: {
  ticked: number;
  shown: number;
  known: Tally[];
  sending: boolean;
  busy: boolean;
  connected: boolean | undefined;
  onAll: () => void;
  onNone: () => void;
  onTag: (add: string[], remove: string[]) => Promise<boolean>;
  onSend: () => void;
  onLeave: () => void;
}) {
  const all = shown > 0 && ticked === shown;
  return (
    <div
      role="region"
      aria-label="Selected books"
      className="sticky bottom-4 z-10 mt-10 flex flex-wrap items-center gap-x-4 gap-y-3 rounded-sm border border-ink bg-sheet px-4 py-3 shadow-[0_10px_24px_-12px_rgb(27_33_30/0.6)]"
    >
      <p className="mr-2 font-semibold">
        {ticked === 0 ? "Tick the books to send to the Kindle" : `${count(ticked, "book")} selected`}
      </p>
      {ticked > 0 && <Categorise ticked={ticked} known={known} onTag={onTag} />}
      {ticked > 0 && (
        <Button variant={sending ? "primary" : "secondary"} disabled={!connected || busy} onClick={onSend}>
          {busy ? "Sending…" : "Send to Kindle"}
        </Button>
      )}
      {ticked > 0 && connected === false && (
        <p className="text-[0.87rem] text-soft">
          No Kindle is connected.{" "}
          <Link to="/kindle" className="underline underline-offset-2">
            Set it up
          </Link>
        </p>
      )}
      <div className="ml-auto flex flex-wrap items-center gap-x-5">
        <Button variant="quiet" onClick={all ? onNone : onAll}>
          {all ? "Select none" : `Select all ${shown} shown`}
        </Button>
        <Button variant="quiet" onClick={onLeave}>
          {sending ? "Cancel" : "Clear"}
        </Button>
      </div>
    </div>
  );
}
